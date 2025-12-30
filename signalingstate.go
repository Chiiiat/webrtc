// SPDX-FileCopyrightText: 2023 The Pion community <https://pion.ly>
// SPDX-License-Identifier: MIT

package webrtc

import (
	"fmt"
	"sync/atomic"

	"github.com/pion/webrtc/v4/pkg/rtcerr"
)

type stateChangeOp int

const (
	stateChangeOpSetLocal  stateChangeOp = iota + 1 // 本地描述设置操作
	stateChangeOpSetRemote                          // 远程描述设置操作
)

// 将状态变更操作类型转换为字符串表示
func (op stateChangeOp) String() string {
	switch op {
	case stateChangeOpSetLocal:
		return "SetLocal"
	case stateChangeOpSetRemote:
		return "SetRemote"
	default:
		return "Unknown State Change Operation"
	}
}

// SignalingState 表示 offer/answer 过程的信令状态
type SignalingState int32

const (
	// SignalingStateUnknown 是枚举的零值，表示未知状态
	SignalingStateUnknown SignalingState = iota

	// SignalingStateStable 表示当前没有正在进行的 offer/answer 交换
	// 这也是初始状态，在这种情况下，本地和远程描述都为 nil
	SignalingStateStable

	// SignalingStateHaveLocalOffer 表示已成功应用了类型为 "offer" 的本地描述
	SignalingStateHaveLocalOffer

	// SignalingStateHaveRemoteOffer 表示已成功应用了类型为 "offer" 的远程描述
	SignalingStateHaveRemoteOffer

	// SignalingStateHaveLocalPranswer 表示已成功应用了类型为 "offer" 的远程描述，
	// 并且已成功应用了类型为 "pranswer" 的本地描述（本地预应答）
	SignalingStateHaveLocalPranswer

	// SignalingStateHaveRemotePranswer 表示已成功应用了类型为 "offer" 的本地描述，
	// 并且已成功应用了类型为 "pranswer" 的远程描述（远程预应答）
	SignalingStateHaveRemotePranswer

	// SignalingStateClosed 表示 PeerConnection 已关闭
	SignalingStateClosed
)

// 这样做是因为代码检查工具的要求
const (
	signalingStateStableStr             = "stable"               // 稳定
	signalingStateHaveLocalOfferStr     = "have-local-offer"     // 拥有本地offer
	signalingStateHaveRemoteOfferStr    = "have-remote-offer"    // 拥有远程offe
	signalingStateHaveLocalPranswerStr  = "have-local-pranswer"  // 拥有本地预应答
	signalingStateHaveRemotePranswerStr = "have-remote-pranswer" // 拥有远程预应答
	signalingStateClosedStr             = "closed"               // 已关闭
)

// 根据字符串表示创建对应的 SignalingState 枚举值
func newSignalingState(raw string) SignalingState {
	switch raw {
	case signalingStateStableStr:
		return SignalingStateStable
	case signalingStateHaveLocalOfferStr:
		return SignalingStateHaveLocalOffer
	case signalingStateHaveRemoteOfferStr:
		return SignalingStateHaveRemoteOffer
	case signalingStateHaveLocalPranswerStr:
		return SignalingStateHaveLocalPranswer
	case signalingStateHaveRemotePranswerStr:
		return SignalingStateHaveRemotePranswer
	case signalingStateClosedStr:
		return SignalingStateClosed
	default:
		return SignalingStateUnknown
	}
}

// 将 SignalingState 枚举值转换为对应的字符串表示
func (t SignalingState) String() string {
	switch t {
	case SignalingStateStable:
		return signalingStateStableStr
	case SignalingStateHaveLocalOffer:
		return signalingStateHaveLocalOfferStr
	case SignalingStateHaveRemoteOffer:
		return signalingStateHaveRemoteOfferStr
	case SignalingStateHaveLocalPranswer:
		return signalingStateHaveLocalPranswerStr
	case SignalingStateHaveRemotePranswer:
		return signalingStateHaveRemotePranswerStr
	case SignalingStateClosed:
		return signalingStateClosedStr
	default:
		return ErrUnknownType.Error()
	}
}

func (t *SignalingState) Get() SignalingState {
	return SignalingState(atomic.LoadInt32((*int32)(t)))
}
func (t *SignalingState) Set(state SignalingState) {
	atomic.StoreInt32((*int32)(t), int32(state))
}

// checkNextSignalingState 状态转换验证函数
/*
cur: 当前状态
next: 下一状态
op: 状态变更操作类型
sdpType: SDP 类型（offer, answer, pranswer, rollback）
*/
func checkNextSignalingState(cur, next SignalingState, op stateChangeOp, sdpType SDPType) (SignalingState, error) {
	// Special case for rollbacks
	if sdpType == SDPTypeRollback && cur == SignalingStateStable {
		return cur, &rtcerr.InvalidModificationError{
			Err: errSignalingStateCannotRollback,
		}
	}

	// 4.3.1 valid state transitions
	switch cur { // nolint:exhaustive
	case SignalingStateStable:
		switch op {
		case stateChangeOpSetLocal:
			// stable->SetLocal(offer)->have-local-offer
			if sdpType == SDPTypeOffer && next == SignalingStateHaveLocalOffer {
				return next, nil
			}
		case stateChangeOpSetRemote:
			// stable->SetRemote(offer)->have-remote-offer
			if sdpType == SDPTypeOffer && next == SignalingStateHaveRemoteOffer {
				return next, nil
			}
		}
	case SignalingStateHaveLocalOffer:
		if op == stateChangeOpSetRemote {
			switch sdpType { // nolint:exhaustive
			// have-local-offer->SetRemote(answer)->stable
			case SDPTypeAnswer:
				if next == SignalingStateStable {
					return next, nil
				}
			// have-local-offer->SetRemote(pranswer)->have-remote-pranswer
			case SDPTypePranswer:
				if next == SignalingStateHaveRemotePranswer {
					return next, nil
				}
			}
		}
	case SignalingStateHaveRemotePranswer:
		if op == stateChangeOpSetRemote && sdpType == SDPTypeAnswer {
			// have-remote-pranswer->SetRemote(answer)->stable
			if next == SignalingStateStable {
				return next, nil
			}
		}
	case SignalingStateHaveRemoteOffer:
		if op == stateChangeOpSetLocal {
			switch sdpType { // nolint:exhaustive
			// have-remote-offer->SetLocal(answer)->stable
			case SDPTypeAnswer:
				if next == SignalingStateStable {
					return next, nil
				}
			// have-remote-offer->SetLocal(pranswer)->have-local-pranswer
			case SDPTypePranswer:
				if next == SignalingStateHaveLocalPranswer {
					return next, nil
				}
			}
		}
	case SignalingStateHaveLocalPranswer:
		if op == stateChangeOpSetLocal && sdpType == SDPTypeAnswer {
			// have-local-pranswer->SetLocal(answer)->stable
			if next == SignalingStateStable {
				return next, nil
			}
		}
	}

	return cur, &rtcerr.InvalidModificationError{
		Err: fmt.Errorf("%w: %s->%s(%s)->%s", errSignalingStateProposedTransitionInvalid, cur, op, sdpType, next),
	}
}
