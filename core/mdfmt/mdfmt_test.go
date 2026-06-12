package mdfmt

import (
	"strings"
	"testing"
)

// format runs Format and asserts idempotency: formatting the output again
// must return it unchanged.
func format(t *testing.T, raw string) string {
	t.Helper()
	out := Format(raw)
	if again := Format(out); again != out {
		t.Errorf("not idempotent:\nonce:  %q\ntwice: %q", out, again)
	}
	return out
}

func TestInsertsBlankLineBetweenVersionAndSectionHeadings(t *testing.T) {
	// Raw @changesets v3 output has no blank line between `## x.y.z` and `### Minor Changes`.
	raw := "# pkg-a\n\n## 1.1.0\n### Minor Changes\n\n- A change\n"

	formatted := format(t, raw)

	want := "# pkg-a\n\n## 1.1.0\n\n### Minor Changes\n\n- A change\n"
	if formatted != want {
		t.Errorf("got %q, want %q", formatted, want)
	}
}

func TestStripsTrailingWhitespace(t *testing.T) {
	raw := "# pkg-a\n\n## 1.1.0\n\n### Minor Changes\n\n- First line.\n  \n  Second paragraph.\n"

	formatted := format(t, raw)

	if strings.Contains(formatted, "  \n") {
		t.Errorf("output contains trailing whitespace: %q", formatted)
	}
	want := "# pkg-a\n\n## 1.1.0\n\n### Minor Changes\n\n- First line.\n\n  Second paragraph.\n"
	if formatted != want {
		t.Errorf("got %q, want %q", formatted, want)
	}
}

func TestCollapsesRunsOfBlankLines(t *testing.T) {
	raw := "# pkg-a\n\n\n\n## 1.1.0\n\n### Patch Changes\n\n- A change\n"

	formatted := format(t, raw)

	want := "# pkg-a\n\n## 1.1.0\n\n### Patch Changes\n\n- A change\n"
	if formatted != want {
		t.Errorf("got %q, want %q", formatted, want)
	}
}

func TestKeepsListItemsTight(t *testing.T) {
	// The dependency-update sublist must stay tight: no blank between the bullet and its nested items, and
	// no blank between adjacent bullets, when nothing in the section is loose.
	raw := "## 1.0.1\n\n### Patch Changes\n\n- Updated dependencies:\n  - pkg-b@1.1.0\n- A direct change\n"

	formatted := format(t, raw)

	want := "## 1.0.1\n\n### Patch Changes\n\n- Updated dependencies:\n  - pkg-b@1.1.0\n- A direct change\n"
	if formatted != want {
		t.Errorf("got %q, want %q", formatted, want)
	}
}

func TestOneMultiParagraphItemMakesWholeSectionLoose(t *testing.T) {
	// CommonMark looseness is per-list: a single multi-paragraph summary turns the whole section loose, so
	// prettier spaces out EVERY bullet in it - including the single-line ones. This is the case the
	// conservative line-based approach got wrong.
	raw := "## 1.1.0\n\n### Minor Changes\n\n- Single line summary.\n- Another summary.\n\n  With a second paragraph.\n"

	formatted := format(t, raw)

	want := "## 1.1.0\n\n### Minor Changes\n\n- Single line summary.\n\n- Another summary.\n\n  With a second paragraph.\n"
	if formatted != want {
		t.Errorf("got %q, want %q", formatted, want)
	}
}

func TestLooseListSpacesBeforeNestedDependencySublist(t *testing.T) {
	// In a loose section the dependency item's text and its nested sublist are separated by a blank line
	// too (prettier's loose-item child spacing).
	raw := "## 1.0.1\n\n### Patch Changes\n\n- A change.\n\n  More detail.\n- Updated dependencies:\n  - pkg-b@1.1.0\n"

	formatted := format(t, raw)

	want := "## 1.0.1\n\n### Patch Changes\n\n- A change.\n\n  More detail.\n\n- Updated dependencies:\n\n  - pkg-b@1.1.0\n"
	if formatted != want {
		t.Errorf("got %q, want %q", formatted, want)
	}
}

func TestAlignsTableColumns(t *testing.T) {
	raw := "## 1.0.0\n\n### Minor Changes\n\n- Adds support:\n\n  | Feature | Status |\n  | :-- | --: |\n  | a | yes |\n  | longer | no |\n"

	formatted := format(t, raw)

	want := "## 1.0.0\n\n### Minor Changes\n\n- Adds support:\n\n  | Feature | Status |\n  | :------ | -----: |\n  | a       |    yes |\n  | longer  |     no |\n"
	if formatted != want {
		t.Errorf("got %q, want %q", formatted, want)
	}
}

func TestAlignsTopLevelTableWithDefaultAlignment(t *testing.T) {
	raw := "| a | bb |\n|-|-|\n| ccc | d |\n"

	formatted := format(t, raw)

	want := "| a   | bb  |\n| --- | --- |\n| ccc | d   |\n"
	if formatted != want {
		t.Errorf("got %q, want %q", formatted, want)
	}
}

func TestLeavesNonTableWithPipesUntouched(t *testing.T) {
	// No valid delimiter row, so this is not a table: it must pass through verbatim, never be mangled.
	raw := "## 1.0.0\n\n### Patch Changes\n\n- Use the `a | b` syntax in code.\n"

	formatted := format(t, raw)

	if formatted != raw {
		t.Errorf("got %q, want %q", formatted, raw)
	}
}

func TestFormatsAGnarlyChangeset(t *testing.T) {
	// Code fence + nested list + blockquote + table inside one loose section - the combined edge fixture.
	raw := "# pkg-a\n\n## 2.0.0\n### Major Changes\n\n- Reworked the API.\n\n  ```\n  before()\n\n  after()\n  ```\n\n  > Note: read the migration guide.\n\n  | Old | New |\n  |--|--|\n  | x | y |\n- Updated dependencies:\n  - pkg-b@1.1.0\n"

	formatted := format(t, raw)

	want := "# pkg-a\n\n## 2.0.0\n\n### Major Changes\n\n- Reworked the API.\n\n  ```\n  before()\n\n  after()\n  ```\n\n  > Note: read the migration guide.\n\n  | Old | New |\n  | --- | --- |\n  | x   | y   |\n\n- Updated dependencies:\n\n  - pkg-b@1.1.0\n"
	if formatted != want {
		t.Errorf("got %q, want %q", formatted, want)
	}
}

func TestEnsuresSingleTrailingNewline(t *testing.T) {
	cases := []struct {
		raw  string
		want string
	}{
		{"# pkg-a\n", "# pkg-a\n"},
		{"# pkg-a", "# pkg-a\n"},
		{"# pkg-a\n\n\n", "# pkg-a\n"},
	}
	for _, tc := range cases {
		if got := format(t, tc.raw); got != tc.want {
			t.Errorf("Format(%q) = %q, want %q", tc.raw, got, tc.want)
		}
	}
}

func TestDropsLeadingBlankLines(t *testing.T) {
	if got := format(t, "\n\n# pkg-a\n"); got != "# pkg-a\n" {
		t.Errorf("got %q, want %q", got, "# pkg-a\n")
	}
}

func TestPreservesBlankLinesInsideCodeFences(t *testing.T) {
	// Prettier preserves the interior of a fenced code block verbatim - including blank lines, which the
	// block-level collapse rule must not touch.
	raw := "## 1.0.0\n\n### Minor Changes\n\n- Example:\n\n  ```\n  line 1\n\n\n  line 2\n  ```\n"

	formatted := format(t, raw)

	if !strings.Contains(formatted, "line 1\n\n\n  line 2") {
		t.Errorf("blank lines inside fence not preserved: %q", formatted)
	}
}

func TestStripsTrailingWhitespaceInsideCodeFences(t *testing.T) {
	raw := "## 1.0.0\n\n### Minor Changes\n\n- Example:\n\n  ```\n  code   \n  ```\n"

	formatted := format(t, raw)

	if strings.Contains(formatted, "code   ") {
		t.Errorf("trailing whitespace inside fence not stripped: %q", formatted)
	}
	if !strings.Contains(formatted, "  code\n") {
		t.Errorf("fence content missing: %q", formatted)
	}
}

func TestSeparatesHeadingFromFollowingFence(t *testing.T) {
	raw := "## 1.0.0\n```\ncode\n```\n"

	formatted := format(t, raw)

	want := "## 1.0.0\n\n```\ncode\n```\n"
	if formatted != want {
		t.Errorf("got %q, want %q", formatted, want)
	}
}

func TestNormalizesCarriageReturns(t *testing.T) {
	raw := "# pkg-a\r\n\r\n## 1.1.0\r\n### Minor Changes\r\n\r\n- A change\r\n"

	formatted := format(t, raw)

	want := "# pkg-a\n\n## 1.1.0\n\n### Minor Changes\n\n- A change\n"
	if formatted != want {
		t.Errorf("got %q, want %q", formatted, want)
	}
}

func TestIsIdempotent(t *testing.T) {
	raw := "# pkg-a\n\n## 1.1.0\n### Minor Changes\n\n- First line.\n  \n  Second paragraph.\n\n### Patch Changes\n\n- Updated dependencies:\n  - pkg-b@1.1.0\n"

	once := format(t, raw)
	twice := Format(once)

	if twice != once {
		t.Errorf("not idempotent:\nonce:  %q\ntwice: %q", once, twice)
	}
}

func TestDoesNotTreatIndentedHashAsHeading(t *testing.T) {
	// A '#' inside a summary is rendered as indented list-item continuation, so it must not be treated as a
	// heading (which would wrongly insert blank lines around it).
	raw := "## 1.0.0\n\n### Minor Changes\n\n- Note:\n  # not a heading\n"

	formatted := format(t, raw)

	if formatted != raw {
		t.Errorf("got %q, want %q", formatted, raw)
	}
}
