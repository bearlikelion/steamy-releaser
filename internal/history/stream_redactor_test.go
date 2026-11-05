package history

import (
	"strings"
	"testing"
)

func TestStreamRedactorProtectsPasswordsSplitAcrossChunks(t *testing.T) {
	for split := 1; split < len("private-password"); split++ {
		var redactor StreamRedactor
		secret := "private-password"
		visible := redactor.Feed("stdout", "echoed "+secret[:split], []string{secret})
		visible += redactor.Feed("stdout", secret[split:]+"\n", []string{secret})
		if visible != "echoed [redacted]\n" {
			t.Fatal("a fragmented password was exposed or output was lost")
		}
	}
}

func TestStreamRedactorKeepsIndependentStreamsAndOrdinaryOutput(t *testing.T) {
	var redactor StreamRedactor
	secrets := []string{"private-password", "fresh-guard-code"}
	visible := redactor.Feed("stdout", "private-", secrets)
	if stderr := redactor.Feed("stderr", "SteamCMD is waiting.\n", secrets); stderr != "SteamCMD is waiting.\n" {
		t.Fatal("independent stream interrupted the pending redaction")
	}
	visible += redactor.Feed("stdout", "password\nfresh-guard-", secrets)
	visible += redactor.Feed("stdout", "code\nComplete", secrets)
	if strings.Contains(visible, "private-password") || strings.Contains(visible, "fresh-guard-code") || !strings.HasSuffix(visible, "Complete") {
		t.Fatal("secret was exposed or ordinary output was lost")
	}
}
