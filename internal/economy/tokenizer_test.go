package economy

import "testing"

func TestCalculateTokenCountsUnicodeClassesForV2(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		text string
		want int64
	}{
		{name: "ascii", text: "abc 123!?\n", want: 10},
		{name: "han", text: "龙虾", want: 4},
		{name: "hiragana", text: "こんにちは", want: 10},
		{name: "hangul", text: "한글", want: 4},
		{name: "emoji", text: "🙂", want: 2},
		{name: "mixed", text: "A龙🙂\n", want: 6},
		{name: "full width punctuation and space", text: "，　\n", want: 3},
		{name: "emoji zwj sequence counts per rune", text: "👨‍👩‍👧‍👦", want: 11},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := CalculateToken(tc.text); got != tc.want {
				t.Fatalf("CalculateToken(%q)=%d want %d", tc.text, got, tc.want)
			}
		})
	}
}
