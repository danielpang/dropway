package hosted

import "testing"

func TestDropwayMention(t *testing.T) {
	for _, body := range []string{"@dropway fix the title", "Please @Dropway, fix this", "(@dropway)"} {
		if !HasDropwayMention(body) {
			t.Errorf("missing mention %q", body)
		}
	}
	for _, body := range []string{"ordinary feedback", "hello@dropway.com", "@dropwayOther", "@dropway-team"} {
		if HasDropwayMention(body) {
			t.Errorf("false mention %q", body)
		}
	}
}
