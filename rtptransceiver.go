// SPDX-FileCopyrightText: 2023 The Pion community <https://pion.ly>
// SPDX-License-Identifier: MIT

//go:build !js
// +build !js

package webrtc

import (
	"fmt"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/pion/rtp"
	"github.com/pion/sdp/v3"
	"github.com/pion/webrtc/v4/internal/fmtp"
)

// RTPTransceiver 双向的媒体通道管理器，可以同时处理发送和接收，或者只处理其中一种
type RTPTransceiver struct {
	mid                    atomic.Value // string - 媒体标识符
	sender                 atomic.Value // *RTPSender - RTP发送器
	receiver               atomic.Value // *RTPReceiver - RTP接收器
	direction              atomic.Value // RTPTransceiverDirection - 转发器传输方向
	currentDirection       atomic.Value // RTPTransceiverDirection - 当前转发器方向
	currentRemoteDirection atomic.Value // RTPTransceiverDirection - 当前远程转发器方向

	codecs []RTPCodecParameters // 用户提供的编解码器偏好

	kind RTPCodecType // 媒体类型（音频或视频）

	api *API // WebRTC API
	mu  sync.RWMutex
}

func newRTPTransceiver(
	receiver *RTPReceiver,
	sender *RTPSender,
	direction RTPTransceiverDirection,
	kind RTPCodecType,
	api *API,
) *RTPTransceiver {
	t := &RTPTransceiver{kind: kind, api: api}
	t.setReceiver(receiver)
	t.setSender(sender)
	t.setDirection(direction)
	t.setCurrentDirection(RTPTransceiverDirectionUnknown)

	return t
}

// SetCodecPreferences 设置支持的编解码器首选列表
// 如果编解码器为空或nil，则重置为MediaEngine中的默认值
func (t *RTPTransceiver) SetCodecPreferences(codecs []RTPCodecParameters) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	for _, codec := range codecs {
		if _, matchType := codecParametersFuzzySearch(
			codec, t.api.mediaEngine.getCodecsByKind(t.kind),
		); matchType == codecMatchNone {
			return fmt.Errorf("%w %s", errRTPTransceiverCodecUnsupported, codec.MimeType)
		}
	}

	t.codecs = filterUnattachedRTX(codecs)

	return nil
}

// getCodecs 返回支持的编解码器列表
// 如果用户没有设置特定偏好，则使用 MediaEngine 中的默认编解码器
// 否则，返回用户设置的编解码器，并与 MediaEngine 中的编解码器进行匹配
func (t *RTPTransceiver) getCodecs() []RTPCodecParameters {
	t.mu.RLock()
	defer t.mu.RUnlock()

	mediaEngineCodecs := t.api.mediaEngine.getCodecsByKind(t.kind)
	if len(t.codecs) == 0 {
		return filterUnattachedRTX(mediaEngineCodecs)
	}

	filteredCodecs := []RTPCodecParameters{}
	for _, codec := range t.codecs {
		if c, matchType := codecParametersFuzzySearch(codec, mediaEngineCodecs); matchType != codecMatchNone {
			if codec.PayloadType == 0 {
				codec.PayloadType = c.PayloadType
			}
			codec.RTCPFeedback = rtcpFeedbackIntersection(codec.RTCPFeedback, c.RTCPFeedback)
			filteredCodecs = append(filteredCodecs, codec)
		}
	}

	return filterUnattachedRTX(filteredCodecs)
}

// setCodecPreferencesFromRemoteDescription
// 从远程描述匹配编解码器，当远程为提议者且根据远程描述创建转发器时使用
// 目的是保持远程描述中的编解码器顺序
func (t *RTPTransceiver) setCodecPreferencesFromRemoteDescription(media *sdp.MediaDescription) { //nolint:cyclop
	remoteCodecs, err := codecsFromMediaDescription(media)
	if err != nil {
		return
	}

	// make a copy as this slice is modified
	leftCodecs := append([]RTPCodecParameters{}, t.api.mediaEngine.getCodecsByKind(t.kind)...)

	// find codec matches between what is in remote description and
	// the transceivers codecs and use payload type registered to
	// media engine.
	payloadMapping := make(map[PayloadType]PayloadType) // for RTX re-mapping later
	filterByMatchType := func(matchFilter codecMatchType) []RTPCodecParameters {
		filteredCodecs := []RTPCodecParameters{}
		for remoteCodecIdx := len(remoteCodecs) - 1; remoteCodecIdx >= 0; remoteCodecIdx-- {
			remoteCodec := remoteCodecs[remoteCodecIdx]
			if strings.EqualFold(remoteCodec.RTPCodecCapability.MimeType, MimeTypeRTX) {
				continue
			}

			matchCodec, matchType := codecParametersFuzzySearch(
				remoteCodec,
				leftCodecs,
			)
			if matchType == matchFilter {
				payloadMapping[remoteCodec.PayloadType] = matchCodec.PayloadType

				remoteCodec.PayloadType = matchCodec.PayloadType
				filteredCodecs = append([]RTPCodecParameters{remoteCodec}, filteredCodecs...)

				// removed matched codec for next round
				remoteCodecs = append(remoteCodecs[:remoteCodecIdx], remoteCodecs[remoteCodecIdx+1:]...)

				needleFmtp := fmtp.Parse(
					matchCodec.RTPCodecCapability.MimeType,
					matchCodec.RTPCodecCapability.ClockRate,
					matchCodec.RTPCodecCapability.Channels,
					matchCodec.RTPCodecCapability.SDPFmtpLine,
				)

				for leftCodecIdx := len(leftCodecs) - 1; leftCodecIdx >= 0; leftCodecIdx-- {
					leftCodec := leftCodecs[leftCodecIdx]
					leftCodecFmtp := fmtp.Parse(
						leftCodec.RTPCodecCapability.MimeType,
						leftCodec.RTPCodecCapability.ClockRate,
						leftCodec.RTPCodecCapability.Channels,
						leftCodec.RTPCodecCapability.SDPFmtpLine,
					)

					if needleFmtp.Match(leftCodecFmtp) {
						leftCodecs = append(leftCodecs[:leftCodecIdx], leftCodecs[leftCodecIdx+1:]...)

						break
					}
				}
			}
		}

		return filteredCodecs
	}

	filteredCodecs := filterByMatchType(codecMatchExact)
	filteredCodecs = append(filteredCodecs, filterByMatchType(codecMatchPartial)...)

	// find RTX associations and add those
	for remotePayloadType, mediaEnginePayloadType := range payloadMapping {
		remoteRTX := findRTXPayloadType(remotePayloadType, remoteCodecs)
		if remoteRTX == PayloadType(0) {
			continue
		}

		mediaEngineRTX := findRTXPayloadType(mediaEnginePayloadType, leftCodecs)
		if mediaEngineRTX == PayloadType(0) {
			continue
		}

		for _, rtxCodec := range leftCodecs {
			if rtxCodec.PayloadType == mediaEngineRTX {
				filteredCodecs = append(filteredCodecs, rtxCodec)

				break
			}
		}
	}
	_ = t.SetCodecPreferences(filteredCodecs)
}

// Sender 返回RTP转发器的RTP发送器（如果有的话）。
func (t *RTPTransceiver) Sender() *RTPSender {
	if v, ok := t.sender.Load().(*RTPSender); ok {
		return v
	}

	return nil
}

// SetSender 将RTP发送器和轨道设置到当前转发器。
func (t *RTPTransceiver) SetSender(s *RTPSender, track TrackLocal) error {
	t.setSender(s)

	return t.setSendingTrack(track)
}
func (t *RTPTransceiver) setSender(s *RTPSender) {
	if s != nil {
		s.setRTPTransceiver(t)
	}

	if prevSender := t.Sender(); prevSender != nil {
		prevSender.setRTPTransceiver(nil)
	}

	t.sender.Store(s)
}

// Receiver 返回RTP转发器的RTP接收器（如果有的话）
func (t *RTPTransceiver) Receiver() *RTPReceiver {
	if v, ok := t.receiver.Load().(*RTPReceiver); ok {
		return v
	}

	return nil
}

// SetMid 设置RTP转发器的mid
// 如果已经设置，则返回错误
func (t *RTPTransceiver) SetMid(mid string) error {
	if currentMid := t.Mid(); currentMid != "" {
		return fmt.Errorf("%w: %s to %s", errRTPTransceiverCannotChangeMid, currentMid, mid)
	}
	t.mid.Store(mid)

	return nil
}

// Mid 获取转发器的mid值
// 如果尚未设置，此值将在CreateOffer或CreateAnswer中设置
func (t *RTPTransceiver) Mid() string {
	if v, ok := t.mid.Load().(string); ok {
		return v
	}

	return ""
}

// Kind 返回RTP转发器的类型（音频或视频）
func (t *RTPTransceiver) Kind() RTPCodecType {
	return t.kind
}

// Direction 返回RTP转发器的当前方向
func (t *RTPTransceiver) Direction() RTPTransceiverDirection {
	if direction, ok := t.direction.Load().(RTPTransceiverDirection); ok {
		return direction
	}

	return RTPTransceiverDirection(0)
}

// Stop 不可逆地停止RTP转发器
// 该方法会停止相关的发送器和接收器，并将方向设置为非活动状态
func (t *RTPTransceiver) Stop() error {
	if sender := t.Sender(); sender != nil {
		if err := sender.Stop(); err != nil {
			return err
		}
	}
	if receiver := t.Receiver(); receiver != nil {
		if err := receiver.Stop(); err != nil {
			return err
		}
	}

	t.setDirection(RTPTransceiverDirectionInactive)
	t.setCurrentDirection(RTPTransceiverDirectionInactive)

	return nil
}

func (t *RTPTransceiver) setReceiver(r *RTPReceiver) {
	if r != nil {
		r.setRTPTransceiver(t)
	}

	if prevReceiver := t.Receiver(); prevReceiver != nil {
		prevReceiver.setRTPTransceiver(nil)
	}

	t.receiver.Store(r)
}

func (t *RTPTransceiver) setDirection(d RTPTransceiverDirection) {
	t.direction.Store(d)
}

func (t *RTPTransceiver) setCurrentDirection(d RTPTransceiverDirection) {
	t.currentDirection.Store(d)
}

func (t *RTPTransceiver) getCurrentDirection() RTPTransceiverDirection {
	if v, ok := t.currentDirection.Load().(RTPTransceiverDirection); ok {
		return v
	}

	return RTPTransceiverDirectionUnknown
}

func (t *RTPTransceiver) setCurrentRemoteDirection(d RTPTransceiverDirection) {
	t.currentRemoteDirection.Store(d)
}

func (t *RTPTransceiver) getCurrentRemoteDirection() RTPTransceiverDirection {
	if v, ok := t.currentRemoteDirection.Load().(RTPTransceiverDirection); ok {
		return v
	}

	return RTPTransceiverDirectionUnknown
}

// setSendingTrack 设置发送轨道，并根据轨道状态和当前方向自动调整转发器的方向
/*
如果有轨道且当前方向是 recvonly → 变为 sendrecv
如果有轨道且当前方向是 inactive → 变为 sendonly
如果无轨道且当前方向是 sendrecv → 变为 recvonly
如果无轨道且当前方向是 sendonly → 变为 inactive
*/
func (t *RTPTransceiver) setSendingTrack(track TrackLocal) error { //nolint:cyclop
	if err := t.Sender().ReplaceTrack(track); err != nil {
		return err
	}
	if track == nil {
		t.setSender(nil)
	}

	switch {
	case track != nil && t.Direction() == RTPTransceiverDirectionRecvonly:
		t.setDirection(RTPTransceiverDirectionSendrecv)
	case track != nil && t.Direction() == RTPTransceiverDirectionInactive:
		t.setDirection(RTPTransceiverDirectionSendonly)
	case track == nil && t.Direction() == RTPTransceiverDirectionSendrecv:
		t.setDirection(RTPTransceiverDirectionRecvonly)
	case track != nil && t.Direction() == RTPTransceiverDirectionSendonly:
		// Handle the case where a sendonly transceiver was added by a negotiation
		// initiated by remote peer. For example a remote peer added a transceiver
		// with direction recvonly.
	case track != nil && t.Direction() == RTPTransceiverDirectionSendrecv:
		// Similar to above, but for sendrecv transceiver.
	case track == nil && t.Direction() == RTPTransceiverDirectionSendonly:
		t.setDirection(RTPTransceiverDirectionInactive)
	default:
		return errRTPTransceiverSetSendingInvalidState
	}

	return nil
}

// isSendAllowed 检查是否允许发送特定类型的媒体
/*
媒体类型是否匹配
是否已有发送器
当前方向是否允许发送
远程方向是否允许发送
*/
func (t *RTPTransceiver) isSendAllowed(kind RTPCodecType) bool {
	if t.kind != kind || t.Sender() != nil {
		return false
	}

	// According to https://www.w3.org/TR/webrtc/#dom-rtcpeerconnection-addtrack, if the
	// transceiver can be reused only if its currentDirection was never sendrecv or sendonly.
	// But that will cause sdp to inflate. So we only check currentDirection's current value,
	// that's worked for all browsers.
	currentDirection := t.getCurrentDirection()
	if currentDirection == RTPTransceiverDirectionSendrecv ||
		currentDirection == RTPTransceiverDirectionSendonly {
		return false
	}

	// `currentRemoteDirection` should be checked before using the transceiver for send.
	// Remote directions could be
	//   - `sendrecv` or `recvonly` - can send, remote direction will transition from
	//     `sendrecv` -> `recvonly` if a remote track was removed.
	//   - `sendonly` or `inactive` - cannot send, remote direction will transitions from
	//     `sendonly` -> `inactive` if a remote track was removed.
	//   - `unknown` - can send - we are the offering side and remote direction is unknown
	currentRemoteDirection := t.getCurrentRemoteDirection()
	if currentRemoteDirection == RTPTransceiverDirectionSendonly ||
		currentRemoteDirection == RTPTransceiverDirectionInactive {
		return false
	}

	return true
}

func findByMid(mid string, localTransceivers []*RTPTransceiver) (*RTPTransceiver, []*RTPTransceiver) {
	for i, t := range localTransceivers {
		if t.Mid() == mid {
			return t, append(localTransceivers[:i], localTransceivers[i+1:]...)
		}
	}

	return nil, localTransceivers
}

// Given a direction+type pluck a transceiver from the passed list
// if no entry satisfies the requested type+direction return a inactive Transceiver.
func satisfyTypeAndDirection(
	remoteKind RTPCodecType,
	remoteDirection RTPTransceiverDirection,
	localTransceivers []*RTPTransceiver,
) (*RTPTransceiver, []*RTPTransceiver) {
	// Get direction order from most preferred to least
	getPreferredDirections := func() []RTPTransceiverDirection {
		switch remoteDirection {
		case RTPTransceiverDirectionSendrecv:
			return []RTPTransceiverDirection{
				RTPTransceiverDirectionRecvonly,
				RTPTransceiverDirectionSendrecv,
				RTPTransceiverDirectionSendonly,
			}
		case RTPTransceiverDirectionSendonly:
			return []RTPTransceiverDirection{RTPTransceiverDirectionRecvonly}
		case RTPTransceiverDirectionRecvonly:
			return []RTPTransceiverDirection{RTPTransceiverDirectionSendonly, RTPTransceiverDirectionSendrecv}
		default:
			return []RTPTransceiverDirection{}
		}
	}

	for _, possibleDirection := range getPreferredDirections() {
		for i := range localTransceivers {
			t := localTransceivers[i]
			if t.Mid() == "" && t.kind == remoteKind && possibleDirection == t.Direction() {
				return t, append(localTransceivers[:i], localTransceivers[i+1:]...)
			}
		}
	}

	return nil, localTransceivers
}

// handleUnknownRTPPacket consumes a single RTP Packet and returns information that is helpful
// for demuxing and handling an unknown SSRC (usually for Simulcast).
func handleUnknownRTPPacket(
	buf []byte,
	midExtensionID,
	streamIDExtensionID,
	repairStreamIDExtensionID uint8,
	mid, rid, rsid *string,
) (paddingOnly bool, err error) {
	rp := &rtp.Packet{}
	if err = rp.Unmarshal(buf); err != nil {
		return false, err
	}

	if rp.Padding && len(rp.Payload) == 0 {
		paddingOnly = true
	}

	if !rp.Header.Extension {
		return paddingOnly, nil
	}

	if payload := rp.GetExtension(midExtensionID); payload != nil {
		*mid = string(payload)
	}

	if payload := rp.GetExtension(streamIDExtensionID); payload != nil {
		*rid = string(payload)
	}

	if payload := rp.GetExtension(repairStreamIDExtensionID); payload != nil {
		*rsid = string(payload)
	}

	return paddingOnly, nil
}
