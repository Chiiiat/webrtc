// SPDX-FileCopyrightText: 2023 The Pion community <https://pion.ly>
// SPDX-License-Identifier: MIT

package webrtc

// SCTPTransportState 表示 SCTP 传输的状态
type SCTPTransportState int

const (
	// SCTPTransportStateUnknown 是枚举的零值
	SCTPTransportStateUnknown SCTPTransportState = iota

	// SCTPTransportStateConnecting 表示 SCTPTransport 正在协商关联
	// 这是创建 SCTPTransport 时 SCTPTransportState 的初始状态
	SCTPTransportStateConnecting

	// SCTPTransportStateConnected 表示关联协商已完成
	SCTPTransportStateConnected

	// SCTPTransportStateClosed 表示已收到 SHUTDOWN 或 ABORT 块
	// 或 SCTP 关联已被有意关闭，例如通过关闭对等连接或应用拒绝数据或更改 SCTP 端口的远程描述
	SCTPTransportStateClosed
)

// This is done this way because of a linter.
const (
	sctpTransportStateConnectingStr = "connecting"
	sctpTransportStateConnectedStr  = "connected"
	sctpTransportStateClosedStr     = "closed"
)

func newSCTPTransportState(raw string) SCTPTransportState {
	switch raw {
	case sctpTransportStateConnectingStr:
		return SCTPTransportStateConnecting
	case sctpTransportStateConnectedStr:
		return SCTPTransportStateConnected
	case sctpTransportStateClosedStr:
		return SCTPTransportStateClosed
	default:
		return SCTPTransportStateUnknown
	}
}

func (s SCTPTransportState) String() string {
	switch s {
	case SCTPTransportStateConnecting:
		return sctpTransportStateConnectingStr
	case SCTPTransportStateConnected:
		return sctpTransportStateConnectedStr
	case SCTPTransportStateClosed:
		return sctpTransportStateClosedStr
	default:
		return ErrUnknownType.Error()
	}
}
