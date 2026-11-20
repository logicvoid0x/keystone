package signer

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/logicvoid0x/keystone/app/protocol/vetypes"
	"github.com/logicvoid0x/keystone/crypto/frost"
	"github.com/logicvoid0x/keystone/crypto/frost/secret"
	"github.com/logicvoid0x/keystone/sidecar/signer/noncestore"
	custodytypes "github.com/logicvoid0x/keystone/x/custody/types"
)

// Spec M4a-T8 nonce-store crash test: the signer process is killed (os.Exit, no
// cleanup) at every step between binding a nonce and sending the VE, then restarted.
// Every restart must send the same share for the session, and the nonce must never be
// bound to a second session.

const (
	envMode  = "SIGNER_CRASH_MODE"
	envDir   = "SIGNER_CRASH_DIR"
	envCrash = "SIGNER_CRASH_AT"
	crashRC  = 3
)

// fixture is shared between the parent and the child processes through files.
type fixture struct {
	GroupKey string `json:"group_key"`
	Share    string `json:"share"` // our share (index 1)
	Y1       string `json:"y1"`
}

type fakeView struct{ v View }

func (f fakeView) Load(map[uint64]uint32) (View, error) { return f.v, nil }

func readJSON(t *testing.T, path string, v any) {
	t.Helper()
	bz, err := os.ReadFile(path)
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(bz, v))
}

func writeJSON(t *testing.T, path string, v any) {
	t.Helper()
	bz, err := json.Marshal(v)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, bz, 0o600))
}

// TestCrashChild is the child process; it does nothing unless envMode is set.
func TestCrashChild(t *testing.T) {
	mode := os.Getenv(envMode)
	if mode == "" {
		t.Skip("child process only")
	}
	dir := os.Getenv(envDir)
	var fx fixture
	readJSON(t, filepath.Join(dir, "fixture.json"), &fx)
	gk, _ := hex.DecodeString(fx.GroupKey)
	sb, _ := hex.DecodeString(fx.Share)
	share, err := frost.ParseScalar(sb)
	require.NoError(t, err)
	store, err := noncestore.Open(noncestore.Path(dir))
	require.NoError(t, err)
	ev := EpochView{Number: 1, GroupKey: gk, Index: 1}
	if mode != "commit" {
		var pool []vetypes.NonceCommitment
		readJSON(t, filepath.Join(dir, "commits.json"), &pool)
		ev.LastRecorded, ev.Pool = 2, pool[1:] // seq 1 was assigned
		var sess custodytypes.SigningSession
		raw, err := os.ReadFile(filepath.Join(dir, mode+".session"))
		require.NoError(t, err)
		require.NoError(t, sess.Unmarshal(raw))
		ev.Sessions = []custodytypes.SigningSession{sess}
	}
	if at := os.Getenv(envCrash); at != "" {
		crashPoint = func(s string) {
			if s == at {
				os.Exit(crashRC) // killed: no deferred cleanup, store not closed
			}
		}
	}
	alarms := 0
	s := New(Config{View: fakeView{View{NoncePool: 8, Epochs: []EpochView{ev}}}, Shares: StaticShares{1: {Index: 1, Secret: share}},
		Store: store, Alarm: func(error) { alarms++ }})
	p, err := s.Payload(10)
	require.NoError(t, err)
	if os.Getenv(envCrash) == "payload" {
		os.Exit(crashRC) // killed after the payload is built, before the VE is sent
	}
	switch mode {
	case "commit":
		writeJSON(t, filepath.Join(dir, "commits.json"), p.Commitments)
	default:
		writeJSON(t, filepath.Join(dir, mode+".out"), map[string]any{"shares": p.Shares, "alarms": alarms})
	}
	require.NoError(t, store.Close())
}

func runChild(t *testing.T, dir, mode, crash string) int {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestCrashChild$", "-test.count=1")
	cmd.Env = append(os.Environ(), envMode+"="+mode, envDir+"="+dir, envCrash+"="+crash)
	out, err := cmd.CombinedOutput()
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		if ee.ExitCode() != crashRC {
			t.Logf("child %s/%s output:\n%s", mode, crash, out)
		}
		return ee.ExitCode()
	}
	require.NoError(t, err, string(out))
	return 0
}

type childOut struct {
	Shares []vetypes.CombinedShare `json:"shares"`
	Alarms int                     `json:"alarms"`
}

func TestNonceStoreCrash(t *testing.T) {
	if testing.Short() {
		t.Skip("spawns processes")
	}
	for _, at := range []string{"bind", "bound", "signed", "payload"} {
		t.Run(at, func(t *testing.T) {
			dir := t.TempDir()
			gk, shares, err := secret.GenerateWithDealer(nil, 3, 2, true)
			require.NoError(t, err)
			s1 := shares[0].Secret.Bytes()
			y1, _ := frost.SerializePoint(shares[0].VerifyingShare)
			writeJSON(t, filepath.Join(dir, "fixture.json"), fixture{GroupKey: hex.EncodeToString(gk[:]), Share: hex.EncodeToString(s1[:]), Y1: hex.EncodeToString(y1[:])})

			// The signer publishes its first commitments (seq 1 and 2).
			require.Zero(t, runChild(t, dir, "commit", ""))
			var pool []vetypes.NonceCommitment
			readJSON(t, filepath.Join(dir, "commits.json"), &pool)
			require.Len(t, pool, 2)

			// Session A assigns it seq 1, with share 2 (held here) as the other signer;
			// session B (a different message) is assigned the same seq.
			other, err := secret.NewNonces(frost.Taproot, nil, shares[1].Secret)
			require.NoError(t, err)
			od, oe, err := other.Commitment()
			require.NoError(t, err)
			mkSession := func(name string) custodytypes.SigningSession {
				id, msg := make([]byte, 32), make([]byte, 32)
				_, _ = rand.Read(id)
				_, _ = rand.Read(msg)
				sess := custodytypes.SigningSession{Id: id, Epoch: 1, Msg: msg, State: custodytypes.SESSION_STATE_OPEN,
					Signers: []custodytypes.SignerAssignment{
						{ShareIndex: 1, Validator: "a", NonceSeq: 1, Hiding: pool[0].Hiding, Binding: pool[0].Binding},
						{ShareIndex: 2, Validator: "b", NonceSeq: 7, Hiding: od[:], Binding: oe[:]},
					}}
				bz, err := sess.Marshal()
				require.NoError(t, err)
				require.NoError(t, os.WriteFile(filepath.Join(dir, name+".session"), bz, 0o600))
				return sess
			}
			a, _ := mkSession("A"), mkSession("B")

			// Killed at the stage, then restarted twice: the same share each time.
			require.Equal(t, crashRC, runChild(t, dir, "A", at))
			require.Zero(t, runChild(t, dir, "A", ""))
			var out1, out2 childOut
			readJSON(t, filepath.Join(dir, "A.out"), &out1)
			require.Zero(t, runChild(t, dir, "A", ""))
			readJSON(t, filepath.Join(dir, "A.out"), &out2)
			require.Len(t, out1.Shares, 1)
			require.Equal(t, out1.Shares, out2.Shares, "a restart re-sends the same share")
			require.Zero(t, out1.Alarms)

			// The share is valid for session A's nonce.
			key, err := frost.NewKey(frost.Taproot, gk[:], nil)
			require.NoError(t, err)
			c1, err := frost.ParseCommitment(1, pool[0].Hiding, pool[0].Binding)
			require.NoError(t, err)
			c2, err := frost.ParseCommitment(2, od[:], oe[:])
			require.NoError(t, err)
			sg, err := frost.NewSigning(key, a.Msg, []frost.Commitment{c1, c2})
			require.NoError(t, err)
			z, err := frost.ParseScalar(out1.Shares[0].Z)
			require.NoError(t, err)
			require.True(t, sg.VerifyShares([]uint32{1}, []frost.Point{shares[0].VerifyingShare}, z))

			// Session B gets the same seq: the signer refuses and raises the alarm.
			require.Zero(t, runChild(t, dir, "B", ""))
			var outB childOut
			readJSON(t, filepath.Join(dir, "B.out"), &outB)
			require.Empty(t, outB.Shares)
			require.Equal(t, 1, outB.Alarms)

			store, err := noncestore.Open(noncestore.Path(dir))
			require.NoError(t, err)
			defer store.Close()
			require.False(t, store.Fresh)
			used, err := store.BoundTo(1, 1, 1)
			require.NoError(t, err)
			require.Equal(t, a.Id, used, "seq 1 is bound to session A only")
		})
	}
}
