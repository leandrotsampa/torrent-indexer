package requester

import "testing"

func Test_normalizeFlareSolverrURL(t *testing.T) {
	tests := []struct {
		name    string
		address string
		want    string
	}{
		{
			name:    "empty",
			address: "",
			want:    "",
		},
		{
			name:    "host only",
			address: "192.168.6.10",
			want:    "http://192.168.6.10:8191",
		},
		{
			name:    "host with scheme",
			address: "http://192.168.6.10",
			want:    "http://192.168.6.10:8191",
		},
		{
			name:    "host with scheme and port",
			address: "http://flaresolverr:8191",
			want:    "http://flaresolverr:8191",
		},
		{
			name:    "trailing slash",
			address: "http://flaresolverr:8191/",
			want:    "http://flaresolverr:8191",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := normalizeFlareSolverrURL(tt.address); got != tt.want {
				t.Fatalf("normalizeFlareSolverrURL() = %q, want %q", got, tt.want)
			}
		})
	}
}
