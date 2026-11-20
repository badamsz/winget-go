package version

import "testing"

func TestCompare(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"1.0", "1.0.0", 0},
		{"1.2", "1.10", -1},
		{"26.06", "26.05", 1},
		{"1.0", "1.0-beta", 1},
		{"1.0-alpha", "1.0-beta", -1},
		{"v2.0", "2.0", 0},
		{"Unknown", "0.0.1", -1},
		{"latest", "99", 1},
		{"0.102.3101", "0.102.3100", 1},
		{"10.0.19041.1", "10.0.19041", 1},
	}
	for _, c := range cases {
		if got := Compare(c.a, c.b); got != c.want {
			t.Errorf("Compare(%q,%q)=%d want %d", c.a, c.b, got, c.want)
		}
		if got := Compare(c.b, c.a); got != -c.want {
			t.Errorf("Compare(%q,%q)=%d want %d", c.b, c.a, got, -c.want)
		}
	}
}

func TestSortDesc(t *testing.T) {
	vs := []string{"1.2", "1.10", "1.9.1", "1.0"}
	SortDesc(vs)
	want := []string{"1.10", "1.9.1", "1.2", "1.0"}
	for i := range vs {
		if vs[i] != want[i] {
			t.Fatalf("got %v want %v", vs, want)
		}
	}
}
