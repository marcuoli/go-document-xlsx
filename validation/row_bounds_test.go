package validation_test

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/marcuoli/go-document-xlsx/internal/testbook"
	"github.com/marcuoli/go-document-xlsx/validation"
)

func TestValidateRowsKeepsArchiveDirectoryInsidePreflightBounds(t *testing.T) {
	for _, mode := range []string{"prefixed", "comment", "wrong central count", "orphan worksheet"} {
		t.Run(mode, func(t *testing.T) {
			sheets := sourceSheets()
			parts := testbook.Parts(sheets)
			if mode == "orphan worksheet" {
				parts["xl/worksheets/orphan.xml"] = parts["xl/worksheets/sheet1.xml"]
			}
			path := filepath.Join(t.TempDir(), "directory.xlsx")
			testbook.Write(t, path, parts)
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			want := ""
			switch mode {
			case "prefixed":
				data = append([]byte("prefix"), data...)
				want = "directory offset differs"
			case "comment":
				binary.LittleEndian.PutUint16(data[len(data)-2:], 7)
				data = append(data, "comment"...)
				want = "directory comment"
			case "wrong central count":
				for _, offset := range []int{8, 10} {
					binary.LittleEndian.PutUint16(data[len(data)-22+offset:], 1)
				}
				want = "directory member coverage"
			case "orphan worksheet":
				want = "unreferenced worksheet part"
			}
			if err := os.WriteFile(path, data, 0600); err != nil {
				t.Fatal(err)
			}
			err = validation.ValidateRows(t.Context(), path, replaySheets(sheets))
			if err == nil || !strings.Contains(err.Error(), want) {
				t.Fatalf("invalid streamed archive: got %v, want %q", err, want)
			}
		})
	}
}
