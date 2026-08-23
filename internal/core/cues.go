package core

import "github.com/LywwKkA-aD/Gul/internal/domain"

// Join and leave cues (PLAN.md 7 M4).
//
// The Mumble layer pushes a whole channel tree on every membership change, so
// "somebody joined" is a diff, not an event: core keeps the set of sessions
// sharing our channel and compares it with the next snapshot. Only that
// channel is watched - a client that beeped for every user on a busy server
// would be unusable - and only other people count: our own arrival is the
// move we just asked for.

// channelCue decides what the new tree snapshot sounds like, and adopts it as
// the baseline for the next one.
//
// Nothing is played when there is no baseline to compare against (the first
// snapshot of a session) or when the baseline describes another channel (we
// moved): both are a new room, and everybody already in it has not just
// arrived.
func (a *App) channelCue(root domain.ChannelNode) (Cue, bool) {
	channelID, members, found := selfChannelMembers(root)

	a.mu.Lock()
	defer a.mu.Unlock()

	if !found {
		// No self in the tree: nothing can be said about our channel, and the
		// next snapshot that carries us starts a fresh baseline.
		a.cueChannel, a.cueMembers, a.cueBaseline = 0, nil, false
		return 0, false
	}

	previous, previousChannel, haveBaseline := a.cueMembers, a.cueChannel, a.cueBaseline
	a.cueChannel, a.cueMembers, a.cueBaseline = channelID, members, true
	if !haveBaseline || previousChannel != channelID {
		return 0, false
	}

	// Arrivals are announced ahead of departures: a snapshot that carries both
	// is one sound to the ear anyway (the engine keeps at most one cue
	// pending), and the person who just walked in is the one worth hearing.
	for session := range members {
		if _, was := previous[session]; !was {
			return CueJoin, true
		}
	}
	for session := range previous {
		if _, still := members[session]; !still {
			return CueLeave, true
		}
	}
	return 0, false
}

// resetChannelCues drops the baseline. Called whenever the session stops being
// connected: a reconnect rebuilds the tree from nothing, and comparing against
// what a dead session last saw would announce everybody twice.
func (a *App) resetChannelCues() {
	a.mu.Lock()
	a.cueChannel, a.cueMembers, a.cueBaseline = 0, nil, false
	a.mu.Unlock()
}

// selfChannelMembers finds the channel containing self and returns the other
// sessions in it. found is false when the snapshot does not carry self, which
// happens between a connection being up and the server having told us who we
// are.
func selfChannelMembers(node domain.ChannelNode) (channelID uint32, members map[uint32]struct{}, found bool) {
	for _, user := range node.Users {
		if !user.IsSelf {
			continue
		}
		members = make(map[uint32]struct{}, len(node.Users)-1)
		for _, other := range node.Users {
			if !other.IsSelf {
				members[other.Session] = struct{}{}
			}
		}
		return node.ID, members, true
	}
	for _, child := range node.Children {
		if id, m, ok := selfChannelMembers(child); ok {
			return id, m, true
		}
	}
	return 0, nil, false
}
