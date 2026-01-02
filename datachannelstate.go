// SPDX-FileCopyrightText: 2023 The Pion community <https://pion.ly>
// SPDX-License-Identifier: MIT

package webrtc

// DataChannelState表示数据通道的状态
type DataChannelState int

const (
	// DataChannelStateUnknown是枚举的零值
	DataChannelStateUnknown DataChannelState = iota

	// DataChannelStateConnecting表示数据通道正在建立中
	// 这是DataChannel的初始状态，无论是使用
	// CreateDataChannel创建，还是作为DataChannelEvent的一部分分发
	DataChannelStateConnecting

	// DataChannelStateOpen表示底层数据传输已建立，可以进行通信
	DataChannelStateOpen

	// DataChannelStateClosing表示
	// 关闭底层数据传输的过程已经开始
	DataChannelStateClosing

	// DataChannelStateClosed表示
	// 底层数据传输已关闭或无法建立
	DataChannelStateClosed
)

// 这样做是因为linter的要求
const (
	dataChannelStateConnectingStr = "connecting"
	dataChannelStateOpenStr       = "open"
	dataChannelStateClosingStr    = "closing"
	dataChannelStateClosedStr     = "closed"
)

func newDataChannelState(raw string) DataChannelState {
	switch raw {
	case dataChannelStateConnectingStr:
		return DataChannelStateConnecting
	case dataChannelStateOpenStr:
		return DataChannelStateOpen
	case dataChannelStateClosingStr:
		return DataChannelStateClosing
	case dataChannelStateClosedStr:
		return DataChannelStateClosed
	default:
		return DataChannelStateUnknown
	}
}

// newDataChannelState根据原始字符串创建DataChannelState值

func (t DataChannelState) String() string {
	switch t {
	case DataChannelStateConnecting:
		return dataChannelStateConnectingStr
	case DataChannelStateOpen:
		return dataChannelStateOpenStr
	case DataChannelStateClosing:
		return dataChannelStateClosingStr
	case DataChannelStateClosed:
		return dataChannelStateClosedStr
	default:
		return ErrUnknownType.Error()
	}
}

// String返回DataChannelState的字符串表示

// MarshalText实现encoding.TextMarshaler接口
func (t DataChannelState) MarshalText() ([]byte, error) {
	return []byte(t.String()), nil
}

// UnmarshalText实现encoding.TextUnmarshaler接口
func (t *DataChannelState) UnmarshalText(b []byte) error {
	*t = newDataChannelState(string(b))

	return nil
}
