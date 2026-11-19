package render_test

import (
	"testing"

	"github.com/inclaro/concordia/internal/assurance/render"
)

func TestParseImageRef(t *testing.T) {
	t.Parallel()

	const digest = "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

	tests := []struct {
		want      render.ImageRef
		name      string
		in        string
		identity  string
		immutable bool
		wantErr   bool
	}{
		{name: "tag", in: "registry.example.com/payments/api:1.2.0",
			want:     render.ImageRef{Registry: "registry.example.com", Repository: "registry.example.com/payments/api", Tag: "1.2.0"},
			identity: "registry.example.com/payments/api:1.2.0"},
		{name: "registry port and tag", in: "registry:5000/app:1.2",
			want:     render.ImageRef{Registry: "registry:5000", Repository: "registry:5000/app", Tag: "1.2"},
			identity: "registry:5000/app:1.2"},
		{name: "registry port and no tag", in: "registry:5000/app",
			want:     render.ImageRef{Registry: "registry:5000", Repository: "registry:5000/app"},
			identity: "registry:5000/app"},
		{name: "digest only", in: "app@" + digest,
			want:      render.ImageRef{Repository: "app", Digest: digest},
			immutable: true, identity: "app@" + digest},
		{name: "tag and digest", in: "app:1.2@" + digest,
			want:      render.ImageRef{Repository: "app", Tag: "1.2", Digest: digest},
			immutable: true, identity: "app@" + digest},
		{name: "no tag", in: "app", want: render.ImageRef{Repository: "app"}, identity: "app"},
		{name: "docker hub namespace", in: "library/nginx:1.27",
			want: render.ImageRef{Repository: "library/nginx", Tag: "1.27"}, identity: "library/nginx:1.27"},
		{name: "localhost registry", in: "localhost/app:dev",
			want: render.ImageRef{Registry: "localhost", Repository: "localhost/app", Tag: "dev"}, identity: "localhost/app:dev"},
		{name: "two tags of one repository differ", in: "registry.example.com/payments/api:1.3.0",
			want:     render.ImageRef{Registry: "registry.example.com", Repository: "registry.example.com/payments/api", Tag: "1.3.0"},
			identity: "registry.example.com/payments/api:1.3.0"},
		{name: "empty", in: "", wantErr: true},
		{name: "unresolved template", in: "{{ .Values.image }}:1", wantErr: true},
		{name: "empty tag", in: "app:", wantErr: true},
		{name: "bad digest", in: "app@sha256:xyz", wantErr: true},
		{name: "white space", in: "app :1", wantErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := render.ParseImageRef(tc.in)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("ParseImageRef(%q) = %+v, want an error", tc.in, got)
				}

				return
			}

			if err != nil {
				t.Fatalf("ParseImageRef(%q): %v", tc.in, err)
			}

			tc.want.Raw = tc.in
			if got != tc.want {
				t.Errorf("ParseImageRef(%q) = %+v, want %+v", tc.in, got, tc.want)
			}

			if got.Immutable() != tc.immutable {
				t.Errorf("Immutable() = %v, want %v", got.Immutable(), tc.immutable)
			}

			if got.Identity() != tc.identity {
				t.Errorf("Identity() = %q, want %q", got.Identity(), tc.identity)
			}
		})
	}
}
