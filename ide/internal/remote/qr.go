package remote

import (
	"encoding/base64"
	"net/url"
	"strings"

	qrcode "github.com/skip2/go-qrcode"
)

// PairingInfo is shown in the IDE modal and consumed by the mobile app.
type PairingInfo struct {
	URI       string   `json:"uri"`
	URLs      []string `json:"urls"`
	Token     string   `json:"token"`
	Host      string   `json:"host"`
	QRDataURL string   `json:"qrDataUrl"`
}

// BuildPairURI builds the hivemind://pair?... payload encoded in the QR code.
// The mobile app saves all URLs and falls back to whichever answers first
// (Tailscale → LAN).
func BuildPairURI(urls []string, token, host string) string {
	q := url.Values{}
	q.Set("urls", strings.Join(urls, ","))
	q.Set("token", token)
	q.Set("host", host)
	return "hivemind://pair?" + q.Encode()
}

// ParsePairURI decodes a hivemind://pair URI into its parts.
func ParsePairURI(uri string) (urls []string, token, host string, ok bool) {
	if !strings.HasPrefix(uri, "hivemind://pair?") {
		return nil, "", "", false
	}
	q, err := url.ParseQuery(strings.TrimPrefix(uri, "hivemind://pair?"))
	if err != nil {
		return nil, "", "", false
	}
	urls = splitComma(q.Get("urls"))
	token = q.Get("token")
	host = q.Get("host")
	return urls, token, host, token != "" && len(urls) > 0
}

func splitComma(s string) []string {
	var out []string
	for _, part := range strings.Split(s, ",") {
		if p := strings.TrimSpace(part); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// QRPNG renders the pairing URI as a PNG QR image.
func QRPNG(uri string, size int) ([]byte, error) {
	if size <= 0 {
		size = 320
	}
	return qrcode.Encode(uri, qrcode.Medium, size)
}

// QRDataURL renders the pairing URI as a data:image/png;base64 URL (IDE modal).
func QRDataURL(uri string, size int) (string, error) {
	png, err := QRPNG(uri, size)
	if err != nil {
		return "", err
	}
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(png), nil
}

// QRASCII renders the pairing URI as a terminal-friendly ASCII QR (`hivemind serve`).
func QRASCII(uri string) (string, error) {
	return qrcode.ToString(uri, qrcode.Medium, false)
}
