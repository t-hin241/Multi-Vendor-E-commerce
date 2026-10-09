// Package policyrules is the readiness contract of AF-02 for rule owners:
// Vendor asks GET /internal/policy-rules/readiness?key=&value= before it
// publishes a policy that cites a rule, and the owner answers ready only
// for a rule version its code enforces, with a hash of the rule and its
// implementation (a new implementation changes the hash).
package policyrules

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"

	"github.com/gin-gonic/gin"

	"shopee/backend/pkg/httpresponse"
)

// Readiness answers for one rule version.
type Readiness func(key, value string) (ready bool, ruleHash, reason string)

// Hash is the rule hash of an enforced rule version.
func Hash(implementation, key, value string) string {
	sum := sha256.Sum256([]byte(implementation + "|" + key + "=" + value))
	return hex.EncodeToString(sum[:])
}

// Handler serves the readiness contract; it never says ready on doubt.
func Handler(readiness Readiness) gin.HandlerFunc {
	return func(c *gin.Context) {
		key, value := c.Query("key"), c.Query("value")
		if key == "" || value == "" || len(key) > 100 || len(value) > 100 {
			httpresponse.Error(c, http.StatusBadRequest, "validation_error", "key and value are required")
			return
		}
		ready, hash, reason := readiness(key, value)
		if !ready {
			hash = ""
		}
		httpresponse.OK(c, http.StatusOK, gin.H{"ready": ready, "rule_hash": hash, "reason": reason})
	}
}
