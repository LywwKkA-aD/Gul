package core

import (
	"testing"

	"github.com/LywwKkA-aD/Gul/internal/domain"
)

// tree builds a snapshot the way the Mumble layer pushes it: a root with two
// channels, self placed in one of them.
func tree(selfChannel uint32, byChannel map[uint32][]uint32) domain.ChannelNode {
	channel := func(id uint32) domain.ChannelNode {
		node := domain.ChannelNode{ID: id, Name: "c"}
		for _, session := range byChannel[id] {
			node.Users = append(node.Users, domain.UserInfo{
				Session: session, Name: "user", ChannelID: id,
			})
		}
		if id == selfChannel {
			node.Users = append(node.Users, domain.UserInfo{
				Session: 1, Name: "self", ChannelID: id, IsSelf: true,
			})
		}
		return node
	}
	root := channel(0)
	root.Children = []domain.ChannelNode{channel(10), channel(20)}
	return root
}

// connected puts the app in a state where a tree snapshot is meaningful.
func connected(app *App) {
	app.HandleStatus(domain.ConnectionStatus{State: domain.StateConnected})
}

func TestChannelCuesAnnounceOurOwnChannelOnly(t *testing.T) {
	t.Parallel()
	app, voice := newVoiceApp(t)
	connected(app)

	// The first snapshot is the baseline: everybody already here has not just
	// arrived.
	app.HandleTree(tree(10, map[uint32][]uint32{10: {2, 3}}))
	if got := voice.snapshot().cues; len(got) != 0 {
		t.Fatalf("cues on the first snapshot = %v, want silence", got)
	}

	app.HandleTree(tree(10, map[uint32][]uint32{10: {2, 3, 4}}))
	app.HandleTree(tree(10, map[uint32][]uint32{10: {2, 4}}))
	// Somebody else's channel is not our business.
	app.HandleTree(tree(10, map[uint32][]uint32{10: {2, 4}, 20: {7}}))
	app.HandleTree(tree(10, map[uint32][]uint32{10: {2, 4}}))

	got := voice.snapshot().cues
	if len(got) != 2 || got[0] != CueJoin || got[1] != CueLeave {
		t.Fatalf("cues = %v, want [CueJoin CueLeave]", got)
	}
}

// Our own move is not an arrival: the room changes, and everybody in the new
// one becomes the baseline.
func TestOurOwnMoveIsSilent(t *testing.T) {
	t.Parallel()
	app, voice := newVoiceApp(t)
	connected(app)

	app.HandleTree(tree(10, map[uint32][]uint32{10: {2}}))
	app.HandleTree(tree(20, map[uint32][]uint32{10: {2}, 20: {5, 6}}))
	if got := voice.snapshot().cues; len(got) != 0 {
		t.Fatalf("cues after moving = %v, want silence", got)
	}

	// The new channel is the baseline from here on.
	app.HandleTree(tree(20, map[uint32][]uint32{10: {2}, 20: {5, 6, 7}}))
	if got := voice.snapshot().cues; len(got) != 1 || got[0] != CueJoin {
		t.Fatalf("cues = %v, want [CueJoin] in the new channel", got)
	}
}

// A reconnect rebuilds the tree from nothing. Comparing it against what the
// dead session last saw would announce the whole channel twice.
func TestReconnectStartsANewBaseline(t *testing.T) {
	t.Parallel()
	app, voice := newVoiceApp(t)
	connected(app)
	app.HandleTree(tree(10, map[uint32][]uint32{10: {2, 3}}))

	app.HandleStatus(domain.ConnectionStatus{State: domain.StateReconnecting})
	app.HandleStatus(domain.ConnectionStatus{State: domain.StateConnected})
	app.HandleTree(tree(10, map[uint32][]uint32{10: {2}}))

	if got := voice.snapshot().cues; len(got) != 0 {
		t.Fatalf("cues after a reconnect = %v, want silence", got)
	}
	app.HandleTree(tree(10, map[uint32][]uint32{10: {2, 3}}))
	if got := voice.snapshot().cues; len(got) != 1 || got[0] != CueJoin {
		t.Fatalf("cues = %v, want the new baseline to work", got)
	}
}

// A snapshot without self says nothing about our channel, and it must not
// leave a baseline behind that the next one would be compared against.
func TestSnapshotWithoutSelfIsSilent(t *testing.T) {
	t.Parallel()
	app, voice := newVoiceApp(t)
	connected(app)
	app.HandleTree(tree(10, map[uint32][]uint32{10: {2}}))

	// 99 is a channel nobody is in, so no user carries IsSelf.
	app.HandleTree(tree(99, map[uint32][]uint32{10: {2, 3}}))
	app.HandleTree(tree(10, map[uint32][]uint32{10: {2, 3}}))

	if got := voice.snapshot().cues; len(got) != 0 {
		t.Fatalf("cues = %v, want silence until a baseline exists again", got)
	}
}

// Arrivals win over departures inside one snapshot: the engine keeps at most
// one cue pending anyway, and the person walking in is the one worth hearing.
func TestOneSnapshotProducesOneCue(t *testing.T) {
	t.Parallel()
	app, voice := newVoiceApp(t)
	connected(app)

	app.HandleTree(tree(10, map[uint32][]uint32{10: {2, 3}}))
	app.HandleTree(tree(10, map[uint32][]uint32{10: {3, 4}}))

	if got := voice.snapshot().cues; len(got) != 1 || got[0] != CueJoin {
		t.Fatalf("cues = %v, want a single CueJoin", got)
	}
}

func TestSelfChannelMembers(t *testing.T) {
	t.Parallel()
	id, members, ok := selfChannelMembers(tree(20, map[uint32][]uint32{10: {2}, 20: {5, 6}}))
	if !ok || id != 20 {
		t.Fatalf("selfChannelMembers = %d, %v, want channel 20", id, ok)
	}
	if len(members) != 2 {
		t.Fatalf("members = %v, want the two others without self", members)
	}
	for _, session := range []uint32{5, 6} {
		if _, in := members[session]; !in {
			t.Errorf("session %d missing from %v", session, members)
		}
	}
	if _, in := members[1]; in {
		t.Error("self counted as a member of its own channel")
	}

	if _, _, ok := selfChannelMembers(domain.ChannelNode{ID: 0}); ok {
		t.Error("an empty tree reported a channel")
	}
}
