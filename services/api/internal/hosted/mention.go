// SPDX-License-Identifier: FSL-1.1-Apache-2.0
package hosted

import "regexp"

var dropwayMention = regexp.MustCompile(`(?i)(^|[\s(])@dropway($|[\s,.!?:;)])`)

// HasDropwayMention recognizes an explicit standalone address, not an email or
// a longer username. The caller must additionally supply authorized run settings.
func HasDropwayMention(body string) bool { return dropwayMention.MatchString(body) }
