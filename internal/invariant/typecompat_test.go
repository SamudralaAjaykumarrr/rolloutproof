package invariant

import "testing"

func TestClassifyTypeChange(t *testing.T) {
	cases := []struct {
		old, new string
		want     typeCompat
	}{
		{"integer", "bigint", typeWidening},
		{"smallint", "integer", typeWidening},
		{"bigint", "integer", typeNarrowing},
		{"integer", "smallint", typeNarrowing},
		{"integer", "integer", typeWidening}, // same rank: not narrower
		{"varchar(50)", "varchar(255)", typeWidening},
		{"varchar(255)", "varchar(50)", typeNarrowing},
		{"varchar(50)", "text", typeWidening},
		{"text", "varchar(50)", typeNarrowing},
		{"text", "varchar", typeWidening}, // unbounded varchar == text
		{"real", "double precision", typeWidening},
		{"double precision", "real", typeNarrowing},
		{"numeric(10,2)", "integer", typeIncomparable},
		{"integer", "boolean", typeIncomparable},
		{"timestamp", "date", typeIncomparable},
	}
	for _, tc := range cases {
		if got := classifyTypeChange(tc.old, tc.new); got != tc.want {
			t.Errorf("classifyTypeChange(%q, %q) = %v, want %v", tc.old, tc.new, got, tc.want)
		}
	}
}
