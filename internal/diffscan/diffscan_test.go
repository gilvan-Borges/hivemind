package diffscan

import (
	"strings"
	"testing"
)

func TestParsePatchAddedLinesAndRanges(t *testing.T) {
	patch := `@@ -10,4 +10,6 @@ func demo() {
 	keep1()
-	oldLine()
+	newLine1()
+	newLine2()
 	keep2()
`
	added, ranges := ParsePatchAddedLines(patch)
	if len(added) != 2 {
		t.Fatalf("expected 2 added lines, got %d (%+v)", len(added), added)
	}
	if added[0].NewLine != 11 || strings.TrimSpace(added[0].Text) != "newLine1()" {
		t.Fatalf("unexpected first added line: %+v", added[0])
	}
	if added[1].NewLine != 12 || strings.TrimSpace(added[1].Text) != "newLine2()" {
		t.Fatalf("unexpected second added line: %+v", added[1])
	}
	if len(ranges) != 1 || ranges[0].Start != 10 || ranges[0].End != 13 {
		t.Fatalf("unexpected ranges: %+v", ranges)
	}
}

func TestAnalyzePRFilesDetectsFindingsAndSkipsTestNoise(t *testing.T) {
	files := []FilePatchInput{
		{
			Filename: "internal/api/handler.go",
			Status:   "modified",
			Patch: `@@ -20,2 +20,5 @@ func handle() {
 	ok()
+	fmt.Println("debug leak")
+	token := "sk-testfake0000111122223333"
+	panic("boom")
 }`,
		},
		{
			Filename: "internal/api/handler_test.go",
			Status:   "modified",
			Patch: `@@ -5,1 +5,3 @@ func TestHandle(t *testing.T) {
+	fmt.Println("test output ok")
+	panic("test panic allowed")
 }`,
		},
	}

	rep := AnalyzePRFiles(files)
	if len(rep.Files) != 2 {
		t.Fatalf("expected 2 file summaries, got %d", len(rep.Files))
	}
	if len(rep.Findings) != 3 {
		t.Fatalf("expected 3 findings in prod code (and 0 from test file), got %d: %+v", len(rep.Findings), rep.Findings)
	}
	if rep.Findings[0].Severity != SeverityP0 || rep.Findings[0].RuleID != "hardcoded-secret" || rep.Findings[0].Line != 22 {
		t.Fatalf("expected P0 hardcoded-secret on line 22 first, got %+v", rep.Findings[0])
	}
	if rep.Findings[1].Severity != SeverityP1 || rep.Findings[1].RuleID != "panic-or-unwrap" {
		t.Fatalf("expected P1 panic-or-unwrap second, got %+v", rep.Findings[1])
	}
	if rep.Findings[2].Severity != SeverityP2 || rep.Findings[2].RuleID != "debug-print" {
		t.Fatalf("expected P2 debug-print third, got %+v", rep.Findings[2])
	}

	section := rep.FormatDossierSection()
	if !strings.Contains(section, "internal/api/handler.go:22") || !strings.Contains(section, "L20-L24") {
		t.Fatalf("unexpected dossier section:\n%s", section)
	}
}

