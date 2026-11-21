package music

import (
	"reflect"
	"testing"
)

func TestDecodeGenres(t *testing.T) {
	cases := []struct {
		in   []string
		want []string
	}{
		{[]string{"(17)"}, []string{"Rock"}},
		{[]string{"17"}, []string{"Rock"}},
		{[]string{"(17)Rock"}, []string{"Rock"}},
		{[]string{"(79)Hard Rock & Metal"}, []string{"Hard Rock & Metal"}},
		{[]string{"(17)(20)"}, []string{"Rock", "Alternative"}},
		{[]string{"(13)(17)Rock"}, []string{"Pop", "Rock"}},
		{[]string{"(RX)(CR)"}, []string{"Remix", "Cover"}},
		{[]string{"((Live)"}, []string{"(Live)"}},
		{[]string{"(Live) Rock"}, []string{"(Live) Rock"}},
		{[]string{"(12)"}, nil},
		{[]string{"Other", "Jazz"}, []string{"Jazz"}},
		{[]string{"(191)", "(192)"}, []string{"Psybient", "(192)"}},
		{[]string{"Rock", "(17)"}, []string{"Rock"}},
		{[]string{"Alternative Rock"}, []string{"Alternative Rock"}},
		{nil, nil},
	}
	for _, c := range cases {
		if got := decodeGenres(c.in); !reflect.DeepEqual(got, c.want) {
			t.Errorf("decodeGenres(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestMapHydrationDecodesNumericGenre(t *testing.T) {
	h := MapHydration(map[string]string{"GENRE": "(12)", "TITLE": "x"}, "/x.mp3")
	if len(h.Genres) != 0 {
		t.Errorf("Genres = %q, want none for the ID3v1 placeholder", h.Genres)
	}
	h = MapHydration(map[string]string{"GENRE": "(17)", "TITLE": "x"}, "/x.mp3")
	if !reflect.DeepEqual(h.Genres, []string{"Rock"}) {
		t.Errorf("Genres = %q, want [Rock]", h.Genres)
	}
}
