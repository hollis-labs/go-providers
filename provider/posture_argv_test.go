package provider

import (
	"slices"
	"strings"
	"testing"

	permission "github.com/hollis-labs/go-permission"
	"github.com/hollis-labs/go-providers/registry"
)

// A posture's flags go at the convention's extra-argument slot, so in every
// native runtime and mode they land before the end-of-options "--" and
// before the prompt: after "--" they would be prompt text (CW-20261001-0102).
// The slot is what the extra arguments use; this holds it to that for each
// posture's actual flags.
func TestPostureFlagsPrecedeThePrompt(t *testing.T) {
	for key, newAdapter := range adapterConstructors {
		desc, ok := registry.Lookup(string(key.id))
		if !ok {
			t.Fatalf("%s is not in the registry", key.id)
		}
		for _, posture := range []permission.Mode{permission.ModeDefault, permission.ModeAcceptEdits, permission.ModePlan, permission.ModeYolo} {
			p, err := desc.PostureFor(posture, key.mode)
			if err != nil {
				t.Errorf("%s/%s %s: %v", key.id, key.mode, posture, err)
				continue
			}
			if len(p.Args) == 0 {
				continue
			}
			adapter := newAdapter()
			switch a := adapter.(type) {
			case *ClaudeAdapter:
				a.ExtraArgs = p.Args
			case *CodexAdapter:
				a.ExtraArgs = p.Args
			case *OpencodeAdapter:
				a.ExtraArgs = p.Args
			case *AntigravityAdapter:
				a.ExtraArgs = p.Args
			default:
				t.Fatalf("%s/%s: %T takes no extra arguments", key.id, key.mode, adapter)
			}
			argv := adapter.BuildArgs("-- say hi", "", "")
			at := -1
			for i := range argv {
				if slices.Equal(argv[i:min(i+len(p.Args), len(argv))], p.Args) {
					at = i
					break
				}
			}
			if at < 0 {
				t.Errorf("%s/%s %s: posture flags %q missing from %q", key.id, key.mode, posture, p.Args, argv)
				continue
			}
			// The prompt starts at "--", or is agy's inline -p=<prompt>; a mode
			// whose turns arrive on stdin or over RPC has none in argv.
			prompt := slices.Index(argv, "--")
			if prompt < 0 {
				prompt = slices.IndexFunc(argv, func(a string) bool { return strings.HasSuffix(a, "-- say hi") })
			}
			if prompt >= 0 && at+len(p.Args) > prompt {
				t.Errorf("%s/%s %s: posture flags after the prompt starts: %q", key.id, key.mode, posture, argv)
			}
		}
	}
}
