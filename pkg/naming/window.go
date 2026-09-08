/*
Copyright 2026 Vikas Verma.

Licensed under the MIT License.
See LICENSE in the project root for license information.
*/

package naming

import (
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"strings"
)

var dnsLabel = regexp.MustCompile(`[^a-z0-9-]+`)

// WindowName is a DNS-1123 name unique per (policy, workload namespace, workload name).
func WindowName(policy, namespace, name string) string {
	raw := strings.ToLower(policy + "-" + namespace + "-" + name)
	raw = dnsLabel.ReplaceAllString(raw, "-")
	raw = strings.Trim(raw, "-")
	if raw == "" {
		raw = "evg"
	}
	if len(raw) <= 63 {
		return raw
	}
	sum := sha256.Sum256([]byte(policy + "/" + namespace + "/" + name))
	suffix := hex.EncodeToString(sum[:8])
	keep := 63 - 1 - len(suffix)
	prefix := strings.Trim(raw[:keep], "-")
	return prefix + "-" + suffix
}
