package mumble

import (
	"sort"

	"github.com/LywwKkA-aD/gumble/gumble"

	"gul/internal/domain"
)

// maxTreeDepth bounds the recursion. Mumble enforces a nesting limit server
// side, so this only exists to keep a malformed or hostile tree from hanging
// the gumble read loop.
const maxTreeDepth = 64

// snapshotTree converts the live gumble channel tree rooted at root into the
// immutable snapshot pushed to the UI.
//
// It is called from gumble listener goroutines, so it only reads already
// resolved in-memory state: no I/O, no locks, no calls back into the client.
// Callers must invoke it from a listener or inside Client.Do - Client and
// everything reachable from it is thread-unsafe by contract.
func snapshotTree(root *gumble.Channel, selfSession uint32) domain.ChannelNode {
	return snapshotChannel(root, selfSession, 0)
}

func snapshotChannel(ch *gumble.Channel, selfSession uint32, depth int) domain.ChannelNode {
	if ch == nil {
		return domain.ChannelNode{
			Users:    []domain.UserInfo{},
			Children: []domain.ChannelNode{},
		}
	}

	node := domain.ChannelNode{
		ID:       ch.ID,
		Name:     ch.Name,
		Position: ch.Position,
		Users:    snapshotUsers(ch.ID, ch.Users, selfSession),
		Children: []domain.ChannelNode{},
	}
	if depth >= maxTreeDepth {
		return node
	}

	children := make([]*gumble.Channel, 0, len(ch.Children))
	for _, child := range ch.Children {
		if child != nil {
			children = append(children, child)
		}
	}
	sort.Slice(children, func(i, j int) bool {
		return lessChannel(children[i], children[j])
	})

	node.Children = make([]domain.ChannelNode, 0, len(children))
	for _, child := range children {
		node.Children = append(node.Children, snapshotChannel(child, selfSession, depth+1))
	}
	return node
}

// snapshotUsers renders the users of one channel in a stable display order.
// channelID comes from the containing channel rather than User.Channel so the
// snapshot stays self-consistent even mid-move.
func snapshotUsers(channelID uint32, users gumble.Users, selfSession uint32) []domain.UserInfo {
	out := make([]domain.UserInfo, 0, len(users))
	for _, u := range users {
		if u == nil {
			continue
		}
		out = append(out, domain.UserInfo{
			Session:   u.Session,
			Hash:      u.Hash,
			Name:      u.Name,
			ChannelID: channelID,
			SelfMute:  u.SelfMuted,
			SelfDeaf:  u.SelfDeafened,
			IsSelf:    u.Session == selfSession,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		return out[i].Session < out[j].Session
	})
	return out
}

// lessChannel orders siblings the way Mumble clients display them: by explicit
// position first, then name, with the ID as a final tie-breaker so the order is
// deterministic across snapshots.
func lessChannel(a, b *gumble.Channel) bool {
	if a.Position != b.Position {
		return a.Position < b.Position
	}
	if a.Name != b.Name {
		return a.Name < b.Name
	}
	return a.ID < b.ID
}
