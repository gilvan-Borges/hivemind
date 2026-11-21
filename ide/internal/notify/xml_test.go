package notify

import "testing"

func TestWindowsToastXMLSilentByDefault(t *testing.T) {
	xml := WindowsToastXML("Plano pronto para aprovar", "revise", "hm-1", false)
	if !containsAll(xml, `launch="hm-1"`, `<audio silent="true"/>`, "Plano pronto para aprovar") {
		t.Fatalf("xml: %s", xml)
	}
	if containsAny(xml, "powershell", "PowerShell") {
		t.Fatal("o toast não pode depender de script")
	}
}

func TestWindowsToastXMLSoundAndEscape(t *testing.T) {
	xml := WindowsToastXML(`a]]>b`, "corpo", `id"1`, true)
	if !containsAll(xml, `launch="id&#34;1"`, "a]] >b", `ms-winsoundevent:Notification.Default`) {
		t.Fatalf("xml: %s", xml)
	}
	if containsAny(xml, `<audio silent="true"/>`) {
		t.Fatal("som ligado não deve silenciar o toast")
	}
}

func containsAll(s string, parts ...string) bool {
	for _, p := range parts {
		if !contains(s, p) {
			return false
		}
	}
	return true
}

func containsAny(s string, parts ...string) bool {
	for _, p := range parts {
		if contains(s, p) {
			return true
		}
	}
	return false
}

func contains(s, part string) bool {
	return len(part) == 0 || (len(s) >= len(part) && (s == part || len(s) > 0 && indexOf(s, part) >= 0))
}

func indexOf(s, part string) int {
	for i := 0; i+len(part) <= len(s); i++ {
		if s[i:i+len(part)] == part {
			return i
		}
	}
	return -1
}
