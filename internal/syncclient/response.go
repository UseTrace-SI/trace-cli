package syncclient

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

func remoteError(response *http.Response) error {
	var body struct {
		Code string `json:"error"`
	}
	_ = json.NewDecoder(io.LimitReader(response.Body, 8192)).Decode(&body)
	switch body.Code {
	case "storage_quota_exceeded":
		return fmt.Errorf("hosted storage quota reached; delete a synced project in the dashboard or contact support")
	case "upload_rate_limited":
		return fmt.Errorf("upload rate limited; automatic sync will retry")
	case "project_deleting":
		return fmt.Errorf("hosted project is being deleted; reconnect only after cleanup finishes")
	case "connection_access_denied", "personal_connection_required":
		return fmt.Errorf("dashboard connection expired or revoked; run trace connect again")
	}
	return fmt.Errorf("sync server rejected request: %s", response.Status)
}
