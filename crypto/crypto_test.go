package crypto

import (
	"fmt"
	"testing"
)

func TestParseKey(t *testing.T) {
	k, err := ParseKey("")
	fmt.Println(k, err)

	k, err = ParseKey(string(TestKey))
	fmt.Println(k, err)

	k, err = ParseKey("jLNu47o6qsBwsqTCI0o60HPOvu5ijGZwnQRMgyDEk9g=")
	fmt.Println(k, err)
}
