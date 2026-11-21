package notify

import (
	"html"
	"strings"
)

// WindowsToastXML is the WinRT toast document. Audio is silent unless sound is set.
// The launch value is the notice id the click handler receives.
func WindowsToastXML(title, body, token string, sound bool) string {
	audio := `<audio silent="true"/>`
	if sound {
		audio = `<audio src="ms-winsoundevent:Notification.Default"/>`
	}
	return `<?xml version="1.0" encoding="UTF-8"?>` +
		`<toast activationType="foreground" duration="short" launch="` + html.EscapeString(token) + `">` +
		`<visual><binding template="ToastGeneric">` +
		`<text hint-maxLines="1"><![CDATA[` + cdata(title) + `]]></text>` +
		`<text><![CDATA[` + cdata(body) + `]]></text>` +
		`</binding></visual>` + audio + `</toast>`
}

func cdata(s string) string {
	return strings.ReplaceAll(s, "]]>", "]] >")
}
