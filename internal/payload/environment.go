package payload

import "strings"

// NodeEnvironment removes injection settings for Kado's private Node runtime.
// User configuration and stdio-server inputs are otherwise preserved.
func NodeEnvironment(values []string) []string {
	result := make([]string, 0, len(values))
	for _, v := range values {
		k := strings.ToUpper(strings.SplitN(v, "=", 2)[0])
		if strings.HasPrefix(k, "NODE_") || strings.HasPrefix(k, "NAPI_") || strings.HasPrefix(k, "BUN_") || strings.HasPrefix(k, "LD_") || strings.HasPrefix(k, "DYLD_") || strings.HasPrefix(k, "NPM_CONFIG_") || k == "OPENSSL_CONF" || k == "OPENSSL_MODULES" {
			continue
		}
		result = append(result, v)
	}
	return result
}
