package core

import (
	"crypto/sha256"
	"encoding/hex"
	"strconv"
)

func AccountID(site string, upstreamUserID int64) string {
	sum := sha256.Sum256([]byte(site + "\n" + strconv.FormatInt(upstreamUserID, 10)))
	return hex.EncodeToString(sum[:])
}
