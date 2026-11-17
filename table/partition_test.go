package table_test

import (
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/marcuoli/go-xlsx-report/table"
)

func TestPartitionNamePreservesIdentityAndBounds(t *testing.T) {
	for _, name := range []string{"Summary", "ação 東京 😀", strings.Repeat("界", 31), strings.Repeat("A", 30) + "x", strings.Repeat("A", 30) + "y"} {
		for _, partition := range []int{1, 2, 99, int(^uint(0) >> 1)} {
			t.Run(fmt.Sprintf("%s/%d", name, partition), func(t *testing.T) {
				got, err := table.PartitionName(name, partition)
				if err != nil {
					t.Fatal(err)
				}
				if err := table.Validate(table.Sheet{Name: got, Headers: []string{"Value"}}); err != nil || !utf8.ValidString(got) || len([]rune(got)) > 31 {
					t.Fatalf("invalid partition name %q: %v", got, err)
				}
				if partition == 1 && got != name || partition > 1 && !strings.HasPrefix(got, fmt.Sprintf("P%d-", partition)) {
					t.Fatalf("partition identity changed: %q", got)
				}
				replayed, err := table.PartitionName(name, partition)
				if err != nil || replayed != got {
					t.Fatalf("unstable name: %q / %q, %v", got, replayed, err)
				}
			})
		}
	}
	first, _ := table.PartitionName(strings.Repeat("A", 30)+"x", 2)
	second, _ := table.PartitionName(strings.Repeat("A", 30)+"y", 2)
	third, _ := table.PartitionName(strings.Repeat("A", 30)+"x", 3)
	if strings.EqualFold(first, second) || strings.EqualFold(first, third) {
		t.Fatalf("long identities or partition numbers collapsed: %q, %q, %q", first, second, third)
	}
	if got, err := table.PartitionName("Summary", 2); err != nil || got != "P2-Summary" {
		t.Fatalf("short name: %q, %v", got, err)
	}
}

func TestPartitionNameRejectsInvalidInputs(t *testing.T) {
	for _, name := range []string{"", " name", "name ", "[]", "a:b", "a/b", "a\\b", "a*b", "a?b", "'name", "name'", "a\nb", strings.Repeat("界", 32)} {
		if got, err := table.PartitionName(name, 1); err == nil || got != "" {
			t.Fatalf("invalid name accepted: %q => %q, %v", name, got, err)
		}
	}
	for _, index := range []int{0, -1} {
		if got, err := table.PartitionName("Summary", index); err == nil || got != "" {
			t.Fatalf("invalid index accepted: %d => %q, %v", index, got, err)
		}
	}
}
