package main

import (
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sathub-client/config"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/rs/zerolog/log"
)

// WSMessage represents a WebSocket message (matches backend structure)
type WSMessage struct {
	Type      string          `json:"type"`
	Payload   json.RawMessage `json:"payload,omitempty"`
	Timestamp time.Time       `json:"timestamp"`
}

// Message types (must match backend)
const (
	MessageTypePing           = "ping"
	MessageTypePong           = "pong"
	MessageTypeSettingsUpdate = "settings_update"
	MessageTypeRestartCommand = "restart_command"
	MessageTypeStatusUpdate   = "status_update"
)

// SettingsUpdatePayload for settings_update messages from server
type SettingsUpdatePayload struct {
	HealthCheckInterval int `json:"health_check_interval"`
	ProcessDelay        int `json:"process_delay"`
}

// StatusUpdatePayload for status_update messages to server
type StatusUpdatePayload struct {
	Version string                 `json:"version"`
	Uptime  int64                  `json:"uptime"` // seconds
	Config  map[string]interface{} `json:"config"`
}

// WSClient manages the WebSocket connection to the backend
type WSClient struct {
	cfg              *config.Config
	configPath       string
	stationID        string
	conn             *websocket.Conn
	mu               sync.RWMutex
	reconnectDelay   time.Duration
	maxReconnectWait time.Duration
	stopChan         chan struct{}
	stopOnce         sync.Once
	sendChan         chan WSMessage
	connected        bool
	startTime        time.Time
	onSettingsUpdate func(*SettingsUpdatePayload)
	onRestart        func()
}

// NewWSClient creates a new WebSocket client
func NewWSClient(cfg *config.Config, configPath string, stationID string) *WSClient {
	return &WSClient{
		cfg:              cfg,
		configPath:       configPath,
		stationID:        stationID,
		reconnectDelay:   5 * time.Second,
		maxReconnectWait: 60 * time.Second,
		stopChan:         make(chan struct{}),
		sendChan:         make(chan WSMessage, 256),
		startTime:        time.Now(),
	}
}

// SetOnSettingsUpdate sets the callback for settings updates
func (ws *WSClient) SetOnSettingsUpdate(callback func(*SettingsUpdatePayload)) {
	ws.onSettingsUpdate = callback
}

// SetOnRestart sets the callback for restart commands
func (ws *WSClient) SetOnRestart(callback func()) {
	ws.onRestart = callback
}

// Connect establishes the WebSocket connection
func (ws *WSClient) Connect() error {
	// Build WebSocket URL from API URL
	wsURL, err := ws.buildWebSocketURL()
	if err != nil {
		return fmt.Errorf("failed to build WebSocket URL: %w", err)
	}

	// Create HTTP header with station token
	header := http.Header{}
	header.Set("Authorization", fmt.Sprintf("Station %s", ws.cfg.Station.Token))

	// Create dialer with TLS config
	dialer := websocket.Dialer{
		HandshakeTimeout: 10 * time.Second,
	}

	if ws.cfg.Options.Insecure {
		dialer.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	}

	// Connect to WebSocket
	log.Info().Str("url", wsURL).Msg("Connecting to WebSocket")
	conn, _, err := dialer.Dial(wsURL, header)
	if err != nil {
		return fmt.Errorf("failed to connect to WebSocket: %w", err)
	}

	ws.mu.Lock()
	ws.conn = conn
	ws.connected = true
	ws.mu.Unlock()

	log.Info().Msg("WebSocket connection established")

	// Start read and write goroutines
	go ws.readPump()
	go ws.writePump()

	return nil
}

// Start initiates the WebSocket connection with auto-reconnect
func (ws *WSClient) Start() {
	go ws.connectWithRetry()
}

// connectWithRetry handles connection with exponential backoff
func (ws *WSClient) connectWithRetry() {
	delay := ws.reconnectDelay

	for {
		select {
		case <-ws.stopChan:
			return
		default:
		}

		err := ws.Connect()
		if err == nil {
			// Reset delay on successful connection
			delay = ws.reconnectDelay
			// Wait for disconnection or stop signal
			ws.waitForDisconnect()
		} else {
			log.Warn().Err(err).Dur("retry_in", delay).Msg("Failed to connect to WebSocket, retrying")

			select {
			case <-ws.stopChan:
				return
			case <-time.After(delay):
			}

			// Exponential backoff
			delay *= 2
			if delay > ws.maxReconnectWait {
				delay = ws.maxReconnectWait
			}
		}
	}
}

// waitForDisconnect blocks until connection is lost or stop signal received
func (ws *WSClient) waitForDisconnect() {
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ws.stopChan:
			return
		case <-ticker.C:
			ws.mu.RLock()
			connected := ws.connected
			ws.mu.RUnlock()

			if !connected {
				return
			}
		}
	}
}

// Stop gracefully shuts down the WebSocket connection
func (ws *WSClient) Stop() {
	ws.stopOnce.Do(func() {
		close(ws.stopChan)
		close(ws.sendChan)

		ws.mu.Lock()
		if ws.conn != nil {
			ws.conn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
			ws.conn.Close()
			ws.conn = nil
		}
		ws.connected = false
		ws.mu.Unlock()

		log.Info().Msg("WebSocket connection closed")
	})
}

// IsConnected returns whether the WebSocket is currently connected
func (ws *WSClient) IsConnected() bool {
	ws.mu.RLock()
	defer ws.mu.RUnlock()
	return ws.connected
}

// Send queues a message to be sent over WebSocket
func (ws *WSClient) Send(msg WSMessage) {
	select {
	case ws.sendChan <- msg:
	case <-ws.stopChan:
	case <-time.After(5 * time.Second):
		log.Warn().Str("type", msg.Type).Msg("Timeout sending WebSocket message")
	}
}

// SendStatusUpdate sends a status update to the server
func (ws *WSClient) SendStatusUpdate() {
	uptime := int64(time.Since(ws.startTime).Seconds())

	payload := StatusUpdatePayload{
		Version: VERSION,
		Uptime:  uptime,
		Config: map[string]interface{}{
			"health_check_interval": ws.cfg.Intervals.HealthCheck,
			"process_delay":         ws.cfg.Intervals.ProcessDelay,
		},
	}

	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		log.Error().Err(err).Msg("Failed to marshal status update")
		return
	}

	ws.Send(WSMessage{
		Type:      MessageTypeStatusUpdate,
		Payload:   payloadJSON,
		Timestamp: time.Now(),
	})
}

// readPump reads messages from the WebSocket
func (ws *WSClient) readPump() {
	defer func() {
		ws.mu.Lock()
		ws.connected = false
		if ws.conn != nil {
			ws.conn.Close()
		}
		ws.mu.Unlock()
	}()

	ws.conn.SetReadDeadline(time.Now().Add(90 * time.Second))
	ws.conn.SetPongHandler(func(string) error {
		ws.conn.SetReadDeadline(time.Now().Add(90 * time.Second))
		return nil
	})
	// PingHandler: gorilla/websocket automatically sends pong responses.
	// We only need to reset the deadline when receiving a ping.
	// The library handles the pong write safely during the read operation.
	ws.conn.SetPingHandler(func(appData string) error {
		err := ws.conn.SetReadDeadline(time.Now().Add(90 * time.Second))
		if err != nil {
			return err
		}
		// Write pong back through the connection's write lock
		// This is safe because control frames can interrupt data frames
		return ws.conn.WriteControl(websocket.PongMessage, []byte(appData), time.Now().Add(time.Second))
	})

	for {
		select {
		case <-ws.stopChan:
			return
		default:
		}

		var msg WSMessage
		err := ws.conn.ReadJSON(&msg)
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				log.Warn().Err(err).Msg("WebSocket unexpected close")
			} else {
				log.Debug().Err(err).Msg("WebSocket read error")
			}
			return
		}

		ws.handleMessage(msg)
	}
}

// writePump writes messages to the WebSocket
func (ws *WSClient) writePump() {
	ticker := time.NewTicker(30 * time.Second)
	defer func() {
		ticker.Stop()
		ws.mu.Lock()
		if ws.conn != nil {
			ws.conn.Close()
		}
		ws.mu.Unlock()
	}()

	for {
		select {
		case <-ws.stopChan:
			return

		case msg, ok := <-ws.sendChan:
			ws.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if !ok {
				ws.conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}

			if err := ws.conn.WriteJSON(msg); err != nil {
				log.Error().Err(err).Msg("Failed to write WebSocket message")
				return
			}

		case <-ticker.C:
			// Send WebSocket-level ping to server
			ws.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := ws.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				log.Error().Err(err).Msg("Failed to send ping")
				return
			}
		}
	}
}

// handleMessage processes incoming WebSocket messages
func (ws *WSClient) handleMessage(msg WSMessage) {
	log.Debug().Str("type", msg.Type).Msg("Received WebSocket message")

	switch msg.Type {
	case MessageTypePong:
		// Server acknowledged our ping
		log.Debug().Msg("Received pong from server")

	case MessageTypePing:
		// Server is pinging us, respond with pong
		ws.Send(WSMessage{
			Type:      MessageTypePong,
			Timestamp: time.Now(),
		})

	case MessageTypeSettingsUpdate:
		// Parse settings update
		var settings SettingsUpdatePayload
		if err := json.Unmarshal(msg.Payload, &settings); err != nil {
			log.Error().Err(err).Msg("Failed to parse settings update")
			return
		}

		log.Info().
			Int("health_check_interval", settings.HealthCheckInterval).
			Int("process_delay", settings.ProcessDelay).
			Msg("Received settings update from server")

		// Call callback if set
		if ws.onSettingsUpdate != nil {
			ws.onSettingsUpdate(&settings)
		}

	case MessageTypeRestartCommand:
		log.Warn().Msg("Received restart command from server")

		// Call callback if set
		if ws.onRestart != nil {
			ws.onRestart()
		}

	default:
		log.Warn().Str("type", msg.Type).Msg("Unknown WebSocket message type")
	}
}

// buildWebSocketURL constructs the WebSocket URL from the API URL
func (ws *WSClient) buildWebSocketURL() (string, error) {
	apiURL := ws.cfg.Station.APIURL

	// Parse the API URL
	u, err := url.Parse(apiURL)
	if err != nil {
		return "", fmt.Errorf("invalid API URL: %w", err)
	}

	// Change scheme to ws or wss
	if u.Scheme == "https" {
		u.Scheme = "wss"
	} else if u.Scheme == "http" {
		u.Scheme = "ws"
	} else {
		return "", fmt.Errorf("unsupported API URL scheme: %s", u.Scheme)
	}

	// Build WebSocket path
	wsPath := fmt.Sprintf("/api/stations/%s/ws", ws.stationID)

	// Remove any existing path and set our WebSocket path
	u.Path = strings.TrimSuffix(u.Path, "/") + wsPath

	return u.String(), nil
}
