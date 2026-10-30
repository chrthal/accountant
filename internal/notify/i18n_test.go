package notify

import (
	"os"
	"regexp"
	"strconv"
	"testing"

	"github.com/chrthal/accountant/internal/i18n"
)

// Every text the notifier mails exists in English too (ADR 0024).
func TestNotifierTextsHaveEnglish(t *testing.T) {
	src, err := os.ReadFile("notifier.go")
	if err != nil {
		t.Fatal(err)
	}
	calls := regexp.MustCompile(`i18n\.Tf?\(lang, ("(?:[^"\\]|\\.)*")`).FindAllSubmatch(src, -1)
	lists := regexp.MustCompile(`store\.\w+Queue, ("(?:[^"\\]|\\.)*")`).FindAllSubmatch(src, -1)
	if len(calls) < 8 || len(lists) != 2 {
		t.Fatalf("found %d texts and %d queue texts; the patterns no longer match notifier.go", len(calls), len(lists))
	}
	for _, m := range append(calls, lists...) {
		msg, err := strconv.Unquote(string(m[1]))
		if err != nil {
			t.Fatal(err)
		}
		if !i18n.Has("en", msg) {
			t.Errorf("no English for %q", msg)
		}
	}
}
