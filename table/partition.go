package table

import (
	"crypto/sha256"
	"errors"
	"fmt"
)

// PartitionName names a one-based partition under the existing 31-character
// worksheet-name contract. Partition one preserves the validated original name;
// later partitions use P<number>- and, when needed, a readable prefix plus a
// stable short SHA-256 suffix of the full original name. It never reserves names:
// callers must check case-insensitive uniqueness across the complete workbook,
// including names from other partition groups and possible short-hash collisions.
func PartitionName(name string, partition int) (string, error) {
	if partition < 1 {
		return "", errors.New("worksheet partition must be one-based")
	}
	if err := Validate(Sheet{Name: name, Headers: []string{""}}); err != nil {
		return "", err
	}
	if partition == 1 {
		return name, nil
	}
	prefix := fmt.Sprintf("P%d-", partition)
	characters := []rune(name)
	if len(prefix)+len(characters) <= 31 {
		return prefix + name, nil
	}
	// Hash the complete identity so names sharing the retained prefix do not
	// collapse through truncation. Global collision checks remain caller-owned.
	digest := sha256.Sum256([]byte(name))
	suffix := fmt.Sprintf("-%x", digest[:4])
	return prefix + string(characters[:31-len(prefix)-len(suffix)]) + suffix, nil
}
