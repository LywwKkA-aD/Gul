package mumble

import (
	"testing"

	"github.com/LywwKkA-aD/gumble/gumble"
)

// newChannel builds a detached gumble channel. Every field the snapshot reads
// is exported, so the mapping is tested against the real types.
func newChannel(id uint32, name string, position int32) *gumble.Channel {
	return &gumble.Channel{
		ID:       id,
		Name:     name,
		Position: position,
		Children: gumble.Channels{},
		Users:    gumble.Users{},
	}
}

func addChild(parent, child *gumble.Channel) {
	child.Parent = parent
	parent.Children[child.ID] = child
}

func addUser(channel *gumble.Channel, u *gumble.User) {
	u.Channel = channel
	channel.Users[u.Session] = u
}

func TestSnapshotTreeMapsChannelsAndUsers(t *testing.T) {
	root := newChannel(0, "Root", 0)
	lounge := newChannel(1, "Lounge", 0)
	addChild(root, lounge)

	addUser(root, &gumble.User{Session: 7, Name: "root-dweller", Hash: "hash-7"})
	addUser(lounge, &gumble.User{
		Session: 42, Name: "self", Hash: "hash-42", SelfMuted: true, SelfDeafened: true,
	})

	tree := snapshotTree(root, 42)

	if tree.ID != 0 || tree.Name != "Root" {
		t.Fatalf("root mapped as id=%d name=%q", tree.ID, tree.Name)
	}
	if len(tree.Children) != 1 {
		t.Fatalf("expected 1 child, got %d", len(tree.Children))
	}

	child := tree.Children[0]
	if child.ID != 1 || child.Name != "Lounge" {
		t.Fatalf("child mapped as id=%d name=%q", child.ID, child.Name)
	}
	if len(child.Users) != 1 {
		t.Fatalf("expected 1 user in Lounge, got %d", len(child.Users))
	}

	self := child.Users[0]
	switch {
	case self.Session != 42:
		t.Errorf("session = %d, want 42", self.Session)
	case self.Hash != "hash-42":
		t.Errorf("hash = %q, want hash-42", self.Hash)
	case self.ChannelID != 1:
		t.Errorf("channelID = %d, want 1", self.ChannelID)
	case !self.SelfMute || !self.SelfDeaf:
		t.Errorf("self mute/deaf = %v/%v, want true/true", self.SelfMute, self.SelfDeaf)
	case !self.IsSelf:
		t.Error("the user matching the session id must be flagged IsSelf")
	}

	if tree.Users[0].IsSelf {
		t.Error("a different session must not be flagged IsSelf")
	}
}

func TestSnapshotTreeOrdersSiblingsByPositionThenName(t *testing.T) {
	root := newChannel(0, "Root", 0)
	addChild(root, newChannel(3, "Zulu", 0))
	addChild(root, newChannel(1, "Alpha", 5))
	addChild(root, newChannel(2, "Bravo", 0))

	tree := snapshotTree(root, 0)

	got := make([]string, 0, len(tree.Children))
	for _, child := range tree.Children {
		got = append(got, child.Name)
	}
	// Position wins; equal positions fall back to name.
	want := []string{"Bravo", "Zulu", "Alpha"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("child order = %v, want %v", got, want)
		}
	}
}

func TestSnapshotTreeOrdersUsersByName(t *testing.T) {
	root := newChannel(0, "Root", 0)
	addUser(root, &gumble.User{Session: 1, Name: "charlie"})
	addUser(root, &gumble.User{Session: 2, Name: "alice"})
	addUser(root, &gumble.User{Session: 3, Name: "bob"})

	tree := snapshotTree(root, 0)

	want := []string{"alice", "bob", "charlie"}
	for i, name := range want {
		if tree.Users[i].Name != name {
			t.Fatalf("user order = %v, want %v", tree.Users, want)
		}
	}
}

func TestSnapshotTreeUsesEmptySlicesNotNil(t *testing.T) {
	// The snapshot is marshalled straight to JSON: nil would become null and
	// force the UI to guard every list.
	tree := snapshotTree(newChannel(0, "Root", 0), 0)
	if tree.Users == nil || tree.Children == nil {
		t.Fatal("empty channels must snapshot as empty slices, not nil")
	}

	empty := snapshotTree(nil, 0)
	if empty.Users == nil || empty.Children == nil {
		t.Fatal("a nil root must still produce a usable node")
	}
}

func TestSnapshotTreeStopsAtDepthLimit(t *testing.T) {
	// A cyclic tree can only come from a broken or hostile server, but the
	// mapping runs on the gumble read loop and must never hang it.
	root := newChannel(0, "Root", 0)
	loop := newChannel(1, "Loop", 0)
	addChild(root, loop)
	loop.Children[loop.ID] = loop

	tree := snapshotTree(root, 0)

	depth := 0
	for node := tree; len(node.Children) > 0; node = node.Children[0] {
		depth++
		if depth > maxTreeDepth+1 {
			t.Fatal("recursion exceeded the depth limit")
		}
	}
	if depth == 0 {
		t.Fatal("expected the tree to be walked at least one level")
	}
}
