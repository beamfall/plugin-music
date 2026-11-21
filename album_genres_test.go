package music

import (
	"reflect"
	"testing"
)

func TestAlbumGenreScraperSharesMusicBrainzThrottle(t *testing.T) {
	spec, ok := AlbumGenreScraper()
	if !ok {
		t.Fatal("AlbumGenreScraper: no musicbrainz scraper in the bundle")
	}
	var base bool
	for _, s := range Bundle().Scrapers {
		if s.ID == spec.ID {
			base = true
			if s.Endpoint != spec.Endpoint || s.MinRequestIntervalMS != spec.MinRequestIntervalMS {
				t.Fatalf("derived spec endpoint/interval = %q/%d, want %q/%d", spec.Endpoint, spec.MinRequestIntervalMS, s.Endpoint, s.MinRequestIntervalMS)
			}
		}
	}
	if !base {
		t.Fatalf("derived spec ID %q is not a bundle scraper", spec.ID)
	}
	if spec.SearchTemplate == "" || spec.LookupTemplate == "" || len(spec.EntityMaps) != 2 {
		t.Fatalf("derived spec incomplete: %+v", spec)
	}
}

func TestGenreKindDeclaresAlbumGenreRelation(t *testing.T) {
	for _, k := range Bundle().EntityKinds {
		if k.Name != "genre" {
			continue
		}
		for _, r := range k.Relations {
			if r.Name == AlbumGenreRelation {
				return
			}
		}
	}
	t.Fatalf("genre kind does not declare %q", AlbumGenreRelation)
}

func TestAlbumGenreQuery(t *testing.T) {
	cases := []struct {
		album, mbid string
		artists     []string
		want        string
	}{
		{"OK Computer", "", []string{"Radiohead"}, `releasegroup:"OK Computer" AND artist:"Radiohead"`},
		{`Say "Hi"`, "", nil, `releasegroup:"Say \"Hi\""`},
		{"Kid A", "0b6b4ba0-d36f-47bd-b4ea-6a5b91842d29", []string{"Radiohead"}, "reid:0b6b4ba0-d36f-47bd-b4ea-6a5b91842d29"},
		{"Kid A", "not-an-mbid", []string{"", "Radiohead"}, `releasegroup:"Kid A" AND artist:"Radiohead"`},
		{"  ", "", []string{"Radiohead"}, ""},
	}
	for _, c := range cases {
		if got := AlbumGenreQuery(c.album, c.mbid, c.artists); got != c.want {
			t.Errorf("AlbumGenreQuery(%q, %q, %q) = %q, want %q", c.album, c.mbid, c.artists, got, c.want)
		}
	}
}

func TestPickAlbumCandidate(t *testing.T) {
	candidates := []AlbumCandidate{
		{ID: "a", Title: "OK Computer OKNOTOK 1997 2017", Artist: "Radiohead"},
		{ID: "b", Title: "OK Computer", Artist: "Radiohead Tribute Band"},
		{ID: "c", Title: "OK Computer", Artist: "Radiohead"},
	}
	if id, ok := PickAlbumCandidate("OK Computer (Collector's Edition)", "", []string{"Radiohead"}, candidates); !ok || id != "c" {
		t.Fatalf("pick = %q,%v, want c", id, ok)
	}
	if id, ok := PickAlbumCandidate("The Bends", "", []string{"Radiohead"}, candidates); ok {
		t.Fatalf("pick on no title match = %q, want none", id)
	}
	if id, ok := PickAlbumCandidate("Anything", "0b6b4ba0-d36f-47bd-b4ea-6a5b91842d29", nil, candidates); !ok || id != "a" {
		t.Fatalf("release-MBID pick = %q,%v, want the first result", id, ok)
	}
	if id, ok := PickAlbumCandidate("Dookie", "", []string{"Green Day"}, []AlbumCandidate{{ID: "d", Title: "Dookie", Artist: "The Green Day"}}); !ok || id != "d" {
		t.Fatalf("leading The pick = %q,%v, want d", id, ok)
	}
}

func TestSelectAlbumGenres(t *testing.T) {
	cases := []struct {
		name  string
		votes []GenreVote
		want  []string
	}{
		{"half of top", []GenreVote{{Name: "alternative rock", Count: 12}, {Name: "art rock", Count: 9}, {Name: "electronic", Count: 2}}, []string{"Alternative Rock", "Art Rock"}},
		{"cap three", []GenreVote{{Name: "a", Count: 5}, {Name: "b", Count: 5}, {Name: "c", Count: 5}, {Name: "d", Count: 5}}, []string{"A", "B", "C"}},
		{"artist fallback", []GenreVote{{Name: "shoegaze", Count: 3, FromArtist: true}}, []string{"Shoegaze"}},
		{"album beats artist", []GenreVote{{Name: "jazz", Count: 1}, {Name: "rock", Count: 9, FromArtist: true}}, []string{"Jazz"}},
		{"casing", []GenreVote{{Name: "r&b", Count: 4}, {Name: "drum and bass", Count: 4}, {Name: "hip-hop", Count: 4}}, []string{"R&B", "Drum and Bass", "Hip-Hop"}},
		{"placeholder", []GenreVote{{Name: "other", Count: 4}}, nil},
		{"none", nil, nil},
	}
	for _, c := range cases {
		if got := SelectAlbumGenres(c.votes); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: SelectAlbumGenres = %q, want %q", c.name, got, c.want)
		}
	}
}

func TestAlbumGenreVotes(t *testing.T) {
	got := AlbumGenreVotes([]AlbumGenreEntity{
		{Relation: AlbumGenreRelation, Name: "rock", Count: "7"},
		{Relation: albumArtistGenreRelation, Name: "pop", Count: "x"},
		{Relation: "other", Name: "skip"},
	})
	want := []GenreVote{{Name: "rock", Count: 7}, {Name: "pop", FromArtist: true}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("AlbumGenreVotes = %+v, want %+v", got, want)
	}
}

func TestGenreTitleUnicode(t *testing.T) {
	if got := genreTitle("électronique"); got != "Électronique" {
		t.Fatalf("genreTitle = %q", got)
	}
}
