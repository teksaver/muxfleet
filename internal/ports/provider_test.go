package ports_test

import (
	"bytes"
	"encoding/json"
	"testing"

	"muxfleet/internal/ports"
)

// TestServerCreateRequestNeverSerializesUserData guards AD-10: user_data holds the
// bootstrap private keys and must never reach a --json output, a journal or a notification.
func TestServerCreateRequestNeverSerializesUserData(t *testing.T) {
	req := ports.ServerCreateRequest{ServerName: "demo", UserData: "bootstrap-secret-material"}
	out, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(out, []byte("bootstrap-secret-material")) || bytes.Contains(out, []byte("user_data")) {
		t.Errorf("ServerCreateRequest JSON leaks user_data: %s", out)
	}
	if !bytes.Contains(out, []byte(`"server_name":"demo"`)) {
		t.Errorf("other fields must still serialize: %s", out)
	}
}
