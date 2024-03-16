package util

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	v1 "k8s.io/api/core/v1"
)

func StringInSlice(str string, strs []string) bool {
	for _, s := range strs {
		if s == str {
			return true
		}
	}
	return false
}

// ParseNodeSelectors parses the node selector string as a JSON array if possible, otherwise it treats it as a literal.
func ParseNodeSelectors(selectors string) ([]string, error) {
	selectors = strings.TrimSpace(selectors)

	if len(selectors) == 0 {
		return nil, errors.New("no node selector provided")
	}

	if strings.HasPrefix(selectors, "[") {
		var parsed []string
		if err := json.Unmarshal([]byte(selectors), &parsed); err != nil {
			return nil, fmt.Errorf("error parsing node selectors: %v", err)
		}
		return parsed, nil
	}
	return []string{selectors}, nil
}

// IsDomainName checks if a string is a presentation-format domain name
// Copied from the stdlib: https://github.com/golang/go/blob/91b8cc0dfaae12af1a89e2b7ad3da10728883ee1/src/net/dnsclient.go#LL75C6-L75C6
func IsDomainName(s string) bool {
	// The root domain name is valid. See golang.org/issue/45715.
	if s == "." {
		return true
	}

	// See RFC 1035, RFC 3696.
	// Presentation format has dots before every label except the first, and the
	// terminal empty label is optional here because we assume fully-qualified
	// (absolute) input. We must therefore reserve space for the first and last
	// labels' length octets in wire format, where they are necessary and the
	// maximum total length is 255.
	// So our _effective_ maximum is 253, but 254 is not rejected if the last
	// character is a dot.
	l := len(s)
	if l == 0 || l > 254 || l == 254 && s[l-1] != '.' {
		return false
	}

	last := byte('.')
	nonNumeric := false // true once we've seen a letter or hyphen
	partlen := 0
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		default:
			return false
		case 'a' <= c && c <= 'z' || 'A' <= c && c <= 'Z' || c == '_':
			nonNumeric = true
			partlen++
		case '0' <= c && c <= '9':
			// fine
			partlen++
		case c == '-':
			// Byte before dash cannot be dot.
			if last == '.' {
				return false
			}
			partlen++
			nonNumeric = true
		case c == '.':
			// Byte before dot cannot be dot, dash.
			if last == '.' || last == '-' {
				return false
			}
			if partlen > 63 || partlen == 0 {
				return false
			}
			partlen = 0
		}
		last = c
	}
	if last == '-' || partlen > 63 {
		return false
	}

	return nonNumeric
}

// GetNodeCondition returns the Node condition of a particular type
// returns the NodeCondition and true if found, otherwise returns an
// empty NodeCondition and false
func GetNodeCondition(node *v1.Node, conditionType v1.NodeConditionType) (v1.NodeCondition, bool) {
	for _, condition := range node.Status.Conditions {
		if condition.Type == conditionType {
			return condition, true
		}
	}

	return v1.NodeCondition{}, false
}
