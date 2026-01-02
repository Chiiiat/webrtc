// SPDX-FileCopyrightText: 2023 The Pion community <https://pion.ly>
// SPDX-License-Identifier: MIT

package webrtc

import (
	"fmt"
	"slices"
	"strings"

	"github.com/pion/sdp/v3"
)

// ICETrickleCapability 表示远程端点是否接受
// 延迟发送的ICE候选者
type ICETrickleCapability int

const (
	// ICETrickleCapabilityUnknown 没有建立远程对等端
	ICETrickleCapabilityUnknown ICETrickleCapability = iota
	// ICETrickleCapabilitySupported 远程对等端可以接受延迟发送的ICE候选者
	ICETrickleCapabilitySupported
	// ICETrickleCapabilitySupported 远程对等端没有声明它可以接受延迟发送的ICE候选者
	ICETrickleCapabilityUnsupported
)

// String 返回ICETrickleCapability的字符串表示
func (t ICETrickleCapability) String() string {
	switch t {
	case ICETrickleCapabilitySupported:
		return "supported"
	case ICETrickleCapabilityUnsupported:
		return "unsupported"
	default:
		return "unknown"
	}
}

// SessionDescription 用于公开本地和远程会话描述
type SessionDescription struct {
	Type SDPType `json:"type"`
	SDP  string  `json:"sdp"`

	// 这永远不会被调用者初始化，仅供内部使用
	parsed *sdp.SessionDescription
}

// Unmarshal 是一个用于反序列化sdp的辅助函数
func (sd *SessionDescription) Unmarshal() (*sdp.SessionDescription, error) {
	sd.parsed = &sdp.SessionDescription{}
	err := sd.parsed.UnmarshalString(sd.SDP)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrSDPUnmarshalling, err)
	}

	return sd.parsed, nil
}

func hasICETrickleOption(desc *sdp.SessionDescription) bool {
	if value, ok := desc.Attribute(sdp.AttrKeyICEOptions); ok && hasTrickleOptionValue(value) {
		return true
	}

	for _, media := range desc.MediaDescriptions {
		if value, ok := media.Attribute(sdp.AttrKeyICEOptions); ok && hasTrickleOptionValue(value) {
			return true
		}
	}

	return false
}

func hasTrickleOptionValue(value string) bool {
	return slices.Contains(strings.Fields(value), "trickle")
}
