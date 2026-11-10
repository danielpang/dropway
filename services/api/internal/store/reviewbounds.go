// SPDX-License-Identifier: FSL-1.1-Apache-2.0
package store

import (
	"encoding/json"
	"errors"
)

const reviewArrayBytes = 256 << 10

var ErrReviewPageTooLarge = errors.New("review item exceeds response size limit")

// boundedReviewRows never splits an item. A byte-limited page has a continuation
// at the last complete item, so callers can retrieve the entire authorized list.
// The HTTP layer separately enforces the final serialized envelope size.
func boundedReviewRows[T any](rows []T, limit int) ([]T, bool, error) {
	used := 2
	for i, row := range rows {
		if i == limit {
			return rows[:i], true, nil
		}
		raw, err := json.Marshal(row)
		if err != nil {
			return nil, false, err
		}
		if used+len(raw)+1 > reviewArrayBytes {
			if i == 0 {
				return nil, false, ErrReviewPageTooLarge
			}
			return rows[:i], true, nil
		}
		used += len(raw) + 1
	}
	return rows, false, nil
}
