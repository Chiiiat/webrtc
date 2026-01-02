// SPDX-FileCopyrightText: 2023 The Pion community <https://pion.ly>
// SPDX-License-Identifier: MIT

package webrtc

// DataChannelInit 可用于配置底层通道的属性，例如数据可靠性
type DataChannelInit struct {
	// Ordered 表示是否允许数据无序传递
	// 默认值为 true，保证数据按顺序传递
	Ordered *bool

	// MaxPacketLifeTime 限制信道在未确认时
	// 传输或重传数据的时间（以毫秒为单位）
	// 如果超出支持的最大值，此值可能会被限制
	MaxPacketLifeTime *uint16

	// MaxRetransmits限制信道在未成功传递数据时重传数据的次数
	// 如果超出支持的最大值，此值可能会被限制
	MaxRetransmits *uint16

	// Protocol描述此通道使用的子协议名称
	Protocol *string

	// Negotiated描述数据通道是由本地对等端还是远程对等端创建
	// 默认值false告诉用户代理在带内（in-band）宣布通道
	// 并指示另一个对等端分发相应的DataChannel
	// 如果设置为true，则由应用程序负责协商通道
	// 并在另一个对等端创建具有相同ID的DataChannel
	Negotiated *bool

	// ID覆盖此通道的默认ID选择
	ID *uint16
}
