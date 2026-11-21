package remote

import (
	"encoding/base64"
	"net/url"
	"strconv"
	"strings"

	qrcode "github.com/skip2/go-qrcode"
)

// PairingInfo is shown in the IDE modal. It carries a one-time offer, never the session token.
type PairingInfo struct {
	URI       string   `json:"uri"`
	URLs      []string `json:"urls"`
	Host      string   `json:"host"`
	QRDataURL string   `json:"qrDataUrl"`
	Exp       int64    `json:"exp"`
}

// BuildPairURI builds the hivemind://pair?v=2 payload encoded in the QR code.
// urls are tried in order. The offer is single-use and is not a session token.
func BuildPairURI(urls []string, offer, pk string, exp int64, host string) string {
	q := url.Values{}
	q.Set("v", "2")
	q.Set("urls", strings.Join(urls, ","))
	q.Set("offer", offer)
	q.Set("pk", pk)
	q.Set("exp", strconv.FormatInt(exp, 10))
	if strings.TrimSpace(host) != "" {
		q.Set("host", host)
	}
	return "hivemind://pair?" + q.Encode()
}

// ParsePairURI decodes a v=2 pairing URI. A token query, including on v=2, is rejected.
func ParsePairURI(uri string) (PairURI, bool) {
	const prefix = "hivemind://pair?"
	if !strings.HasPrefix(uri, prefix) {
		return PairURI{}, false
	}
	q, err := url.ParseQuery(strings.TrimPrefix(uri, prefix))
	if err != nil {
		return PairURI{}, false
	}
	if q.Get("token") != "" || q.Get("v") != "2" {
		return PairURI{}, false
	}
	urls := splitComma(q.Get("urls"))
	offerRaw, errOffer := base64.RawURLEncoding.DecodeString(q.Get("offer"))
	pkRaw, errPK := base64.RawURLEncoding.DecodeString(q.Get("pk"))
	exp, errExp := strconv.ParseInt(q.Get("exp"), 10, 64)
	if errOffer != nil || len(offerRaw) != 16 || errPK != nil || len(pkRaw) != 32 || errExp != nil || exp <= 0 || len(urls) == 0 {
		return PairURI{}, false
	}
	return PairURI{
		URLs:  urls,
		Offer: q.Get("offer"),
		PK:    q.Get("pk"),
		Exp:   exp,
		Host:  q.Get("host"),
	}, true
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
	q, err := qrcode.New(uri, qrcode.Medium)
	if err != nil {
		return "", err
	}
	return q.ToString(false), nil
}
