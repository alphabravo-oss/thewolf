package routes

import (
	"testing"

	"github.com/alphabravocompany/thewolf/internal/models"
)

func TestSortFindingsTiesRemainDeterministic(t *testing.T) {
	for _, order := range []string{"asc", "desc"} {
		findings := []models.Finding{{ID: "c", Title: "same"}, {ID: "a", Title: "same"}, {ID: "b", Title: "same"}}
		sortFindings(findings, "title", order)
		for i, id := range []string{"a", "b", "c"} {
			if findings[i].ID != id {
				t.Fatalf("%s: unstable tie ordering: %+v", order, findings)
			}
		}
	}
}
