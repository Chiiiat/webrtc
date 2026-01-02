// SPDX-FileCopyrightText: 2023 The Pion community <https://pion.ly>
// SPDX-License-Identifier: MIT

package webrtc

// SCTPCapabilities 表示 SCTPTransport 的功能
type SCTPCapabilities struct {
	MaxMessageSize uint32 `json:"maxMessageSize"`
}
