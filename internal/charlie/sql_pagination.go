package charlie

import (
	"fmt"

	"github.com/alphabravocompany/astronomer-go/pkg/safeconv"
)

// capabilitySQLPage keeps the existing Charlie schema limits beside the SQL
// narrowing boundary. Callers retain native page/size metadata without clamping.
func capabilitySQLPage(page, size int64) (int32, int32, error) {
	if page < 1 || page > 10000 || size < 1 || size > 100 {
		return 0, 0, fmt.Errorf("capability pagination is out of bounds")
	}
	// The accepted maximum offset is 999900, safely below the SQL int32 limit.
	return int32(size), safeconv.Int32((page - 1) * size), nil
}
