package eval

import (
	"encoding/json"
	"fmt"
	"os"
)



// LoadTestCases reads test cases from a JSON file.
func LoadTestCases(path string) ([]*TestCase, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read test cases: %w", err)
	}
	var cases []*TestCase
	if err := json.Unmarshal(b, &cases); err != nil {
		return nil, fmt.Errorf("unmarshal test cases: %w", err)
	}
	return cases, nil
}
