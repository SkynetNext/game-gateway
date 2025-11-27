package protocol

// ProtocolType represents the protocol type
type ProtocolType int

const (
	ProtocolHTTP ProtocolType = iota
	ProtocolWebSocket
	ProtocolTCP
)

// ParseServiceType parses service type from protocol header
// Returns service_type, realm_id, user_id, session_id, error
func ParseServiceType(data []byte) (serviceType string, realmID int32, userID int64, sessionID int64, err error) {
	// TODO: Implement protocol parsing
	// For now, return defaults
	return "", 0, 0, 0, nil
}
