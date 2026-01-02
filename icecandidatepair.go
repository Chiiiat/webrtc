// SPDX-FileCopyrightText: 2023 The Pion community <https://pion.ly>
// SPDX-License-Identifier: MIT

package webrtc

import "fmt"

// ICECandidatePair 表示一个 ICE 候选对
type ICECandidatePair struct {
	statsID string
	Local   *ICECandidate
	Remote  *ICECandidate
}

func newICECandidatePairStatsID(localID, remoteID string) string {
	return fmt.Sprintf("%s-%s", localID, remoteID)
}

func (p *ICECandidatePair) String() string {
	return fmt.Sprintf("(local) %s <-> (remote) %s", p.Local, p.Remote)
}

// NewICECandidatePair 返回一个初始化的 *ICECandidatePair
// 用于给定的 ICECandidate 实例对
func NewICECandidatePair(local, remote *ICECandidate) *ICECandidatePair {
	statsID := newICECandidatePairStatsID(local.statsID, remote.statsID)

	return &ICECandidatePair{
		statsID: statsID,
		Local:   local,
		Remote:  remote,
	}
}
