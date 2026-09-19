package protocol

import "time"

const Version = "v1"

type CredentialRequest struct {
	ClientRequestID string `json:"client_request_id"`
	RepositoryName  string `json:"repository_name"`
	EstimatedBytes  int64  `json:"estimated_bytes"`
	FileCount       int64  `json:"file_count"`
}

type UploadForm struct {
	Method    string            `json:"method"`
	URL       string            `json:"url"`
	FileField string            `json:"file_field"`
	Fields    map[string]string `json:"fields"`
}

type CredentialResponse struct {
	ProtocolVersion string     `json:"protocol_version"`
	SnapshotID      string     `json:"snapshot_id"`
	PublicKeyPEM    string     `json:"public_key_pem"`
	MaxSize         int64      `json:"max_size"`
	ExpiresAt       time.Time  `json:"expires_at"`
	Upload          UploadForm `json:"upload"`
}

type CallbackReceipt struct {
	SnapshotID string    `json:"snapshot_id"`
	ObjectKey  string    `json:"object_key"`
	Size       int64     `json:"size"`
	ETag       string    `json:"etag,omitempty"`
	ReceivedAt time.Time `json:"received_at"`
}

type APIError struct {
	Error string `json:"error"`
}
