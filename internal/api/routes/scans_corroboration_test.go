package routes

import (
	"testing"

	"github.com/alphabravocompany/thewolf/internal/models"
)

func TestClusterMatchesDB(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		db      models.Finding
		cluster models.Finding
		want    bool
	}{
		{
			name:    "sca same rule",
			db:      models.Finding{Category: models.CategorySCA, RuleID: "CVE-2024-1", FilePath: "go.mod"},
			cluster: models.Finding{Category: models.CategorySCA, RuleID: "CVE-2024-1", FilePath: "pkg/mod"},
			want:    true,
		},
		{
			name:    "sca different rule",
			db:      models.Finding{Category: models.CategorySCA, RuleID: "CVE-2024-1"},
			cluster: models.Finding{Category: models.CategorySCA, RuleID: "CVE-2024-2"},
			want:    false,
		},
		{
			name:    "sca empty rule does not match",
			db:      models.Finding{Category: models.CategorySCA},
			cluster: models.Finding{Category: models.CategorySCA},
			want:    false,
		},
		{
			name: "sast fine category",
			db: models.Finding{
				Category: models.CategorySAST, FilePath: "auth.go", LineStart: 10, FineCategory: "sql-injection",
			},
			cluster: models.Finding{
				Category: models.CategorySAST, FilePath: "auth.go", LineStart: 10, FineCategory: "sql-injection",
			},
			want: true,
		},
		{
			name: "sast rule fallback",
			db: models.Finding{
				Category: models.CategorySAST, FilePath: "auth.go", LineStart: 10, RuleID: "G101",
			},
			cluster: models.Finding{
				Category: models.CategorySAST, FilePath: "auth.go", LineStart: 10, RuleID: "G101",
			},
			want: true,
		},
		{
			name: "sast different line",
			db: models.Finding{
				Category: models.CategorySAST, FilePath: "auth.go", LineStart: 11, RuleID: "G101",
			},
			cluster: models.Finding{
				Category: models.CategorySAST, FilePath: "auth.go", LineStart: 10, RuleID: "G101",
			},
			want: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := clusterMatchesDB(tt.db, tt.cluster); got != tt.want {
				t.Fatalf("clusterMatchesDB() = %v, want %v", got, tt.want)
			}
		})
	}
}
