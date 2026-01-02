// SPDX-FileCopyrightText: 2023 The Pion community <https://pion.ly>
// SPDX-License-Identifier: MIT

//go:build !js
// +build !js

package webrtc

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/pion/rtp"
	"github.com/pion/rtp/codecs"
	"github.com/pion/sdp/v3"
	"github.com/pion/webrtc/v4/internal/fmtp"
)

// mediaEngineHeaderExtension RTP 头扩展（如 mid、abs-send-time 等）
type mediaEngineHeaderExtension struct {
	uri              string
	isAudio, isVideo bool

	// 如果设置了该字段，则只有该方向的 Transceiver 才被允许
	allowedDirections []RTPTransceiverDirection
}

// MediaEngine 定义了 PeerConnection 支持的编解码器，以及这些编解码器的配置
type MediaEngine struct {
	// 标记是否已经尝试协商过某种编解码器类型
	negotiatedVideo, negotiatedAudio bool
	negotiateMultiCodecs             bool

	videoCodecs, audioCodecs                     []RTPCodecParameters
	negotiatedVideoCodecs, negotiatedAudioCodecs []RTPCodecParameters

	headerExtensions           []mediaEngineHeaderExtension
	negotiatedHeaderExtensions map[int]mediaEngineHeaderExtension

	mu sync.RWMutex
}

// setMultiCodecNegotiation 启用或禁用多编解码器协商能力
func (m *MediaEngine) setMultiCodecNegotiation(negotiateMultiCodecs bool) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.negotiateMultiCodecs = negotiateMultiCodecs
}

// multiCodecNegotiation 返回当前是否开启多编解码器协商能力
func (m *MediaEngine) multiCodecNegotiation() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return m.negotiateMultiCodecs
}

// RegisterDefaultCodecs 注册 Pion WebRTC 默认支持的一组编解码器
// RegisterDefaultCodecs 不是并发安全的，不要在多 goroutine 中同时调用
func (m *MediaEngine) RegisterDefaultCodecs() error {
	// Pion 默认音频编解码器（Opus/G722/PCMU/PCMA）
	for _, codec := range []RTPCodecParameters{
		{
			RTPCodecCapability: RTPCodecCapability{MimeTypeOpus, 48000, 2, "minptime=10;useinbandfec=1", nil},
			PayloadType:        111,
		},
		{
			RTPCodecCapability: RTPCodecCapability{MimeTypeG722, 8000, 0, "", nil},
			PayloadType:        rtp.PayloadTypeG722,
		},
		{
			RTPCodecCapability: RTPCodecCapability{MimeTypePCMU, 8000, 0, "", nil},
			PayloadType:        rtp.PayloadTypePCMU,
		},
		{
			RTPCodecCapability: RTPCodecCapability{MimeTypePCMA, 8000, 0, "", nil},
			PayloadType:        rtp.PayloadTypePCMA,
		},
	} {
		if err := m.RegisterCodec(codec, RTPCodecTypeAudio); err != nil {
			return err
		}
	}

	// Pion 默认视频编解码器（VP8 + RTX/H264 + RTX/H264 + RTX/AV1 + RTX）
	videoRTCPFeedback := []RTCPFeedback{{"goog-remb", ""}, {"ccm", "fir"}, {"nack", ""}, {"nack", "pli"}}
	for _, codec := range []RTPCodecParameters{
		{
			RTPCodecCapability: RTPCodecCapability{MimeTypeVP8, 90000, 0, "", videoRTCPFeedback},
			PayloadType:        96,
		},
		{
			RTPCodecCapability: RTPCodecCapability{MimeTypeRTX, 90000, 0, "apt=96", nil},
			PayloadType:        97,
		},

		{
			RTPCodecCapability: RTPCodecCapability{
				MimeTypeH264, 90000, 0,
				"level-asymmetry-allowed=1;packetization-mode=1;profile-level-id=42001f",
				videoRTCPFeedback,
			},
			PayloadType: 102,
		},
		{
			RTPCodecCapability: RTPCodecCapability{MimeTypeRTX, 90000, 0, "apt=102", nil},
			PayloadType:        103,
		},

		{
			RTPCodecCapability: RTPCodecCapability{
				MimeTypeH264, 90000, 0,
				"level-asymmetry-allowed=1;packetization-mode=0;profile-level-id=42001f",
				videoRTCPFeedback,
			},
			PayloadType: 104,
		},
		{
			RTPCodecCapability: RTPCodecCapability{MimeTypeRTX, 90000, 0, "apt=104", nil},
			PayloadType:        105,
		},

		{
			RTPCodecCapability: RTPCodecCapability{
				MimeTypeH264, 90000, 0,
				"level-asymmetry-allowed=1;packetization-mode=1;profile-level-id=42e01f",
				videoRTCPFeedback,
			},
			PayloadType: 106,
		},
		{
			RTPCodecCapability: RTPCodecCapability{MimeTypeRTX, 90000, 0, "apt=106", nil},
			PayloadType:        107,
		},

		{
			RTPCodecCapability: RTPCodecCapability{
				MimeTypeH264, 90000, 0,
				"level-asymmetry-allowed=1;packetization-mode=0;profile-level-id=42e01f",
				videoRTCPFeedback,
			},
			PayloadType: 108,
		},
		{
			RTPCodecCapability: RTPCodecCapability{MimeTypeRTX, 90000, 0, "apt=108", nil},
			PayloadType:        109,
		},

		{
			RTPCodecCapability: RTPCodecCapability{
				MimeTypeH264, 90000, 0,
				"level-asymmetry-allowed=1;packetization-mode=1;profile-level-id=4d001f",
				videoRTCPFeedback,
			},
			PayloadType: 127,
		},
		{
			RTPCodecCapability: RTPCodecCapability{MimeTypeRTX, 90000, 0, "apt=127", nil},
			PayloadType:        125,
		},

		{
			RTPCodecCapability: RTPCodecCapability{
				MimeTypeH264,
				90000, 0,
				"level-asymmetry-allowed=1;packetization-mode=0;profile-level-id=4d001f",
				videoRTCPFeedback,
			},
			PayloadType: 39,
		},
		{
			RTPCodecCapability: RTPCodecCapability{MimeTypeRTX, 90000, 0, "apt=39", nil},
			PayloadType:        40,
		},
		{
			RTPCodecCapability: RTPCodecCapability{
				MimeType:     MimeTypeH265,
				ClockRate:    90000,
				RTCPFeedback: videoRTCPFeedback,
			},
			PayloadType: 116,
		},
		{
			RTPCodecCapability: RTPCodecCapability{MimeTypeRTX, 90000, 0, "apt=116", nil},
			PayloadType:        117,
		},
		{
			RTPCodecCapability: RTPCodecCapability{MimeTypeAV1, 90000, 0, "", videoRTCPFeedback},
			PayloadType:        45,
		},
		{
			RTPCodecCapability: RTPCodecCapability{MimeTypeRTX, 90000, 0, "apt=45", nil},
			PayloadType:        46,
		},

		{
			RTPCodecCapability: RTPCodecCapability{MimeTypeVP9, 90000, 0, "profile-id=0", videoRTCPFeedback},
			PayloadType:        98,
		},
		{
			RTPCodecCapability: RTPCodecCapability{MimeTypeRTX, 90000, 0, "apt=98", nil},
			PayloadType:        99,
		},

		{
			RTPCodecCapability: RTPCodecCapability{MimeTypeVP9, 90000, 0, "profile-id=2", videoRTCPFeedback},
			PayloadType:        100,
		},
		{
			RTPCodecCapability: RTPCodecCapability{MimeTypeRTX, 90000, 0, "apt=100", nil},
			PayloadType:        101,
		},

		{
			RTPCodecCapability: RTPCodecCapability{
				MimeTypeH264, 90000, 0,
				"level-asymmetry-allowed=1;packetization-mode=1;profile-level-id=64001f",
				videoRTCPFeedback,
			},
			PayloadType: 112,
		},
		{
			RTPCodecCapability: RTPCodecCapability{MimeTypeRTX, 90000, 0, "apt=112", nil},
			PayloadType:        113,
		},
	} {
		if err := m.RegisterCodec(codec, RTPCodecTypeVideo); err != nil {
			return err
		}
	}

	return nil
}

// addCodec 会在不存在该编解码器时将其追加到列表中
func (m *MediaEngine) addCodec(codecs []RTPCodecParameters, codec RTPCodecParameters) ([]RTPCodecParameters, error) {
	for _, c := range codecs {
		if c.PayloadType == codec.PayloadType {
			if strings.EqualFold(c.MimeType, codec.MimeType) &&
				fmtp.ClockRateEqual(c.MimeType, c.ClockRate, codec.ClockRate) &&
				fmtp.ChannelsEqual(c.MimeType, c.Channels, codec.Channels) {
				return codecs, nil
			}

			return codecs, ErrCodecAlreadyRegistered
		}
	}

	return append(codecs, codec), nil
}

// RegisterCodec 将编解码器添加到 MediaEngine 中
// 这些就是当前 PeerConnection 所支持的编解码器列表
func (m *MediaEngine) RegisterCodec(codec RTPCodecParameters, typ RTPCodecType) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	var err error
	codec.statsID = fmt.Sprintf("RTPCodec-%d", time.Now().UnixNano())
	switch typ {
	case RTPCodecTypeAudio:
		m.audioCodecs, err = m.addCodec(m.audioCodecs, codec)
	case RTPCodecTypeVideo:
		m.videoCodecs, err = m.addCodec(m.videoCodecs, codec)
	default:
		return ErrUnknownType
	}

	return err
}

// RegisterHeaderExtension 向 MediaEngine 注册一个 RTP 头部扩展
// 要获取协商出的扩展 ID，可在信令完成后调用 `GetHeaderExtensionID`
//
//nolint:cyclop
func (m *MediaEngine) RegisterHeaderExtension(
	extension RTPHeaderExtensionCapability,
	typ RTPCodecType,
	allowedDirections ...RTPTransceiverDirection,
) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.negotiatedHeaderExtensions == nil {
		m.negotiatedHeaderExtensions = map[int]mediaEngineHeaderExtension{}
	}

	if len(allowedDirections) == 0 {
		allowedDirections = []RTPTransceiverDirection{RTPTransceiverDirectionRecvonly, RTPTransceiverDirectionSendonly}
	}

	for _, direction := range allowedDirections {
		if direction != RTPTransceiverDirectionRecvonly && direction != RTPTransceiverDirectionSendonly {
			return ErrRegisterHeaderExtensionInvalidDirection
		}
	}

	extensionIndex := -1
	for i := range m.headerExtensions {
		if extension.URI == m.headerExtensions[i].uri {
			extensionIndex = i
		}
	}

	if extensionIndex == -1 {
		m.headerExtensions = append(m.headerExtensions, mediaEngineHeaderExtension{})
		extensionIndex = len(m.headerExtensions) - 1
	}

	if typ == RTPCodecTypeAudio {
		m.headerExtensions[extensionIndex].isAudio = true
	} else if typ == RTPCodecTypeVideo {
		m.headerExtensions[extensionIndex].isVideo = true
	}

	m.headerExtensions[extensionIndex].uri = extension.URI
	m.headerExtensions[extensionIndex].allowedDirections = allowedDirections

	return nil
}

// RegisterFeedback 为已注册的编解码器增加 RTCP 反馈机制
func (m *MediaEngine) RegisterFeedback(feedback RTCPFeedback, typ RTPCodecType) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if typ == RTPCodecTypeVideo {
		for i, v := range m.videoCodecs {
			v.RTCPFeedback = append(v.RTCPFeedback, feedback)
			m.videoCodecs[i] = v
		}
	} else if typ == RTPCodecTypeAudio {
		for i, v := range m.audioCodecs {
			v.RTCPFeedback = append(v.RTCPFeedback, feedback)
			m.audioCodecs[i] = v
		}
	}
}

// getHeaderExtensionID returns the negotiated ID for a header extension.
// If the Header Extension isn't enabled ok will be false.
func (m *MediaEngine) getHeaderExtensionID(extension RTPHeaderExtensionCapability) (
	val int,
	audioNegotiated, videoNegotiated bool,
) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.negotiatedHeaderExtensions == nil {
		return 0, false, false
	}

	for id, h := range m.negotiatedHeaderExtensions {
		if extension.URI == h.uri {
			return id, h.isAudio, h.isVideo
		}
	}

	return
}

// copy 复制 MediaEngine 中用户可修改的状态，所有内部运行时状态都会被重置
func (m *MediaEngine) copy() *MediaEngine {
	m.mu.Lock()
	defer m.mu.Unlock()
	cloned := &MediaEngine{
		videoCodecs:      append([]RTPCodecParameters{}, m.videoCodecs...),
		audioCodecs:      append([]RTPCodecParameters{}, m.audioCodecs...),
		headerExtensions: append([]mediaEngineHeaderExtension{}, m.headerExtensions...),
	}
	if len(m.headerExtensions) > 0 {
		cloned.negotiatedHeaderExtensions = map[int]mediaEngineHeaderExtension{}
	}

	return cloned
}

func findCodecByPayload(codecs []RTPCodecParameters, payloadType PayloadType) *RTPCodecParameters {
	for _, codec := range codecs {
		if codec.PayloadType == payloadType {
			return &codec
		}
	}

	return nil
}

func (m *MediaEngine) getCodecByPayload(payloadType PayloadType) (RTPCodecParameters, RTPCodecType, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	// 如果已经协商出音频或视频编解码器，
	// 则优先在协商后的集合中查找，而不是依赖内置的 payload 类型，
	// 以保证选择到对端真正期望的编解码器
	if m.negotiatedVideo {
		if codec := findCodecByPayload(m.negotiatedVideoCodecs, payloadType); codec != nil {
			return *codec, RTPCodecTypeVideo, nil
		}
	}
	if m.negotiatedAudio {
		if codec := findCodecByPayload(m.negotiatedAudioCodecs, payloadType); codec != nil {
			return *codec, RTPCodecTypeAudio, nil
		}
	}
	if !m.negotiatedVideo {
		if codec := findCodecByPayload(m.videoCodecs, payloadType); codec != nil {
			return *codec, RTPCodecTypeVideo, nil
		}
	}
	if !m.negotiatedAudio {
		if codec := findCodecByPayload(m.audioCodecs, payloadType); codec != nil {
			return *codec, RTPCodecTypeAudio, nil
		}
	}

	return RTPCodecParameters{}, 0, ErrCodecNotFound
}

func (m *MediaEngine) collectStats(collector *statsReportCollector) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	statsLoop := func(codecs []RTPCodecParameters) {
		for _, codec := range codecs {
			collector.Collecting()
			stats := CodecStats{
				Timestamp:   statsTimestampFrom(time.Now()),
				Type:        StatsTypeCodec,
				ID:          codec.statsID,
				PayloadType: codec.PayloadType,
				MimeType:    codec.MimeType,
				ClockRate:   codec.ClockRate,
				Channels:    uint8(codec.Channels), //nolint:gosec // G115
				SDPFmtpLine: codec.SDPFmtpLine,
			}

			collector.Collect(stats.ID, stats)
		}
	}

	statsLoop(m.videoCodecs)
	statsLoop(m.audioCodecs)
}

// 查找并启用某个编解码器（如果本地支持该编解码器）
//
//nolint:cyclop
func (m *MediaEngine) matchRemoteCodec(
	remoteCodec RTPCodecParameters,
	typ RTPCodecType,
	exactMatches, partialMatches []RTPCodecParameters,
) (RTPCodecParameters, codecMatchType, error) {
	codecs := m.videoCodecs
	if typ == RTPCodecTypeAudio {
		codecs = m.audioCodecs
	}

	remoteFmtp := fmtp.Parse(
		remoteCodec.RTPCodecCapability.MimeType,
		remoteCodec.RTPCodecCapability.ClockRate,
		remoteCodec.RTPCodecCapability.Channels,
		remoteCodec.RTPCodecCapability.SDPFmtpLine)

	if apt, hasApt := remoteFmtp.Parameter("apt"); hasApt { //nolint:nestif
		payloadType, err := strconv.ParseUint(apt, 10, 8)
		if err != nil {
			return RTPCodecParameters{}, codecMatchNone, err
		}

		aptMatch := codecMatchNone
		var aptCodec RTPCodecParameters
		for _, codec := range exactMatches {
			if codec.PayloadType == PayloadType(payloadType) {
				aptMatch = codecMatchExact
				aptCodec = codec

				break
			}
		}

		if aptMatch == codecMatchNone {
			for _, codec := range partialMatches {
				if codec.PayloadType == PayloadType(payloadType) {
					aptMatch = codecMatchPartial
					aptCodec = codec

					break
				}
			}
		}

		if aptMatch == codecMatchNone {
			return RTPCodecParameters{}, codecMatchNone, nil // not an error, we just ignore this codec we don't support
		}

		// replace the apt value with the original codec's payload type
		toMatchCodec := remoteCodec
		if aptMatched, mt := codecParametersFuzzySearch(aptCodec, codecs); mt == aptMatch {
			toMatchCodec.SDPFmtpLine = strings.Replace(
				toMatchCodec.SDPFmtpLine,
				fmt.Sprintf("apt=%d", payloadType),
				fmt.Sprintf("apt=%d", aptMatched.PayloadType),
				1,
			)
		}

		// if apt's media codec is partial match, then apt codec must be partial match too.
		localCodec, matchType := codecParametersFuzzySearch(toMatchCodec, codecs)
		if matchType == codecMatchExact && aptMatch == codecMatchPartial {
			matchType = codecMatchPartial
		}

		return localCodec, matchType, nil
	}

	localCodec, matchType := codecParametersFuzzySearch(remoteCodec, codecs)

	return localCodec, matchType, nil
}

// 从远端的媒体描述（m-line）中更新 RTP 头部扩展配置
func (m *MediaEngine) updateHeaderExtensionFromMediaSection(media *sdp.MediaDescription) error {
	var typ RTPCodecType
	switch {
	case strings.EqualFold(media.MediaName.Media, "audio"):
		typ = RTPCodecTypeAudio
	case strings.EqualFold(media.MediaName.Media, "video"):
		typ = RTPCodecTypeVideo
	default:
		return nil
	}
	extensions, err := rtpExtensionsFromMediaDescription(media)
	if err != nil {
		return err
	}

	for extension, id := range extensions {
		if err = m.updateHeaderExtension(id, extension, typ); err != nil {
			return err
		}
	}

	return nil
}

// 查找某个头部扩展并在本地启用它（如果存在）
func (m *MediaEngine) updateHeaderExtension(id int, extension string, typ RTPCodecType) error {
	if m.negotiatedHeaderExtensions == nil {
		return nil
	}

	for _, localExtension := range m.headerExtensions {
		if localExtension.uri == extension {
			h := mediaEngineHeaderExtension{uri: extension, allowedDirections: localExtension.allowedDirections}
			if existingValue, ok := m.negotiatedHeaderExtensions[id]; ok {
				h = existingValue
			}

			switch {
			case localExtension.isAudio && typ == RTPCodecTypeAudio:
				h.isAudio = true
			case localExtension.isVideo && typ == RTPCodecTypeVideo:
				h.isVideo = true
			}

			m.negotiatedHeaderExtensions[id] = h
		}
	}

	return nil
}

func (m *MediaEngine) pushCodecs(codecs []RTPCodecParameters, typ RTPCodecType) error {
	var joinedErr error
	for _, codec := range codecs {
		var err error
		if typ == RTPCodecTypeAudio {
			m.negotiatedAudioCodecs, err = m.addCodec(m.negotiatedAudioCodecs, codec)
		} else if typ == RTPCodecTypeVideo {
			m.negotiatedVideoCodecs, err = m.addCodec(m.negotiatedVideoCodecs, codec)
		}
		if err != nil {
			joinedErr = errors.Join(joinedErr, err)
		}
	}

	return joinedErr
}

// 根据远端的 SDP 描述，更新本地 MediaEngine 的协商结果
func (m *MediaEngine) updateFromRemoteDescription(desc sdp.SessionDescription) error { //nolint:cyclop,gocognit
	m.mu.Lock()
	defer m.mu.Unlock()

	for _, media := range desc.MediaDescriptions {
		var typ RTPCodecType

		switch {
		case strings.EqualFold(media.MediaName.Media, "audio"):
			typ = RTPCodecTypeAudio
		case strings.EqualFold(media.MediaName.Media, "video"):
			typ = RTPCodecTypeVideo
		}

		switch {
		case !m.negotiatedAudio && typ == RTPCodecTypeAudio:
			m.negotiatedAudio = true
		case !m.negotiatedVideo && typ == RTPCodecTypeVideo:
			m.negotiatedVideo = true
		default:
			// 如果对应类型的编解码器已经协商过，则只更新头部扩展
			// 比如 Firefox 在重新协商时可能发送新的头部扩展：
			// 例如：先发布一条不带 simulcast 的 track -> 协商完成 -> 再发布一条带 simulcast 的 track，
			// 此时两个媒体描述在 offer 中就会有不同的 RTP 头部扩展，需要在这里同步更新
			if err := m.updateHeaderExtensionFromMediaSection(media); err != nil {
				return err
			}

			if !m.negotiateMultiCodecs || (typ != RTPCodecTypeAudio && typ != RTPCodecTypeVideo) {
				continue
			}
		}

		codecs, err := codecsFromMediaDescription(media)
		if err != nil {
			return err
		}

		addIfNew := func(existingCodecs []RTPCodecParameters, codec RTPCodecParameters) []RTPCodecParameters {
			found := false
			for _, existingCodec := range existingCodecs {
				if existingCodec.PayloadType == codec.PayloadType {
					found = true

					break
				}
			}

			if !found {
				existingCodecs = append(existingCodecs, codec)
			}

			return existingCodecs
		}

		exactMatches := make([]RTPCodecParameters, 0, len(codecs))
		partialMatches := make([]RTPCodecParameters, 0, len(codecs))

		for _, remoteCodec := range codecs {
			localCodec, matchType, mErr := m.matchRemoteCodec(remoteCodec, typ, exactMatches, partialMatches)
			if mErr != nil {
				return mErr
			}

			remoteCodec.RTCPFeedback = rtcpFeedbackIntersection(localCodec.RTCPFeedback, remoteCodec.RTCPFeedback)

			if matchType == codecMatchExact {
				exactMatches = addIfNew(exactMatches, remoteCodec)
			} else if matchType == codecMatchPartial {
				partialMatches = addIfNew(partialMatches, remoteCodec)
			}
		}
		// 第二次遍历，防止漏掉 RTX 之类的编解码器
		for _, remoteCodec := range codecs {
			localCodec, matchType, mErr := m.matchRemoteCodec(remoteCodec, typ, exactMatches, partialMatches)
			if mErr != nil {
				return mErr
			}

			remoteCodec.RTCPFeedback = rtcpFeedbackIntersection(localCodec.RTCPFeedback, remoteCodec.RTCPFeedback)

			if matchType == codecMatchExact {
				exactMatches = addIfNew(exactMatches, remoteCodec)
			} else if matchType == codecMatchPartial {
				partialMatches = addIfNew(partialMatches, remoteCodec)
			}
		}

		// 如果存在完全匹配的编解码器，就优先使用完全匹配；否则退而求其次使用部分匹配
		switch {
		case len(exactMatches) > 0:
			err = m.pushCodecs(exactMatches, typ)
		case len(partialMatches) > 0:
			err = m.pushCodecs(partialMatches, typ)
		default:
			// 没有任何匹配的编解码器，本条 m-line 视为未协商成功
			continue
		}
		if err != nil {
			return err
		}

		if err := m.updateHeaderExtensionFromMediaSection(media); err != nil {
			return err
		}
	}

	return nil
}

func (m *MediaEngine) getCodecsByKind(typ RTPCodecType) []RTPCodecParameters {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if typ == RTPCodecTypeVideo {
		if m.negotiatedVideo {
			return m.negotiatedVideoCodecs
		}

		return m.videoCodecs
	} else if typ == RTPCodecTypeAudio {
		if m.negotiatedAudio {
			return m.negotiatedAudioCodecs
		}

		return m.audioCodecs
	}

	return nil
}

//nolint:gocognit,cyclop
func (m *MediaEngine) getRTPParametersByKind(typ RTPCodecType, directions []RTPTransceiverDirection) RTPParameters {
	headerExtensions := make([]RTPHeaderExtensionParameter, 0)

	// 在加锁之前执行，避免递归 RLock 的问题
	foundCodecs := m.getCodecsByKind(typ)

	m.mu.RLock()
	defer m.mu.RUnlock()

	//nolint:nestif
	if (m.negotiatedVideo && typ == RTPCodecTypeVideo) || (m.negotiatedAudio && typ == RTPCodecTypeAudio) {
		for id, e := range m.negotiatedHeaderExtensions {
			if haveRTPTransceiverDirectionIntersection(e.allowedDirections, directions) &&
				(e.isAudio && typ == RTPCodecTypeAudio || e.isVideo && typ == RTPCodecTypeVideo) {
				headerExtensions = append(headerExtensions, RTPHeaderExtensionParameter{ID: id, URI: e.uri})
			}
		}
	} else {
		mediaHeaderExtensions := make(map[int]mediaEngineHeaderExtension)
		for _, ext := range m.headerExtensions {
			usingNegotiatedID := false
			for id := range m.negotiatedHeaderExtensions {
				if m.negotiatedHeaderExtensions[id].uri == ext.uri {
					usingNegotiatedID = true
					mediaHeaderExtensions[id] = ext

					break
				}
			}
			if !usingNegotiatedID {
				for id := 1; id < 15; id++ {
					idAvailable := true
					if _, ok := mediaHeaderExtensions[id]; ok {
						idAvailable = false
					}
					if _, taken := m.negotiatedHeaderExtensions[id]; idAvailable && !taken {
						mediaHeaderExtensions[id] = ext

						break
					}
				}
			}
		}

		for id, e := range mediaHeaderExtensions {
			if haveRTPTransceiverDirectionIntersection(e.allowedDirections, directions) &&
				(e.isAudio && typ == RTPCodecTypeAudio || e.isVideo && typ == RTPCodecTypeVideo) {
				headerExtensions = append(headerExtensions, RTPHeaderExtensionParameter{ID: id, URI: e.uri})
			}
		}
	}

	return RTPParameters{
		HeaderExtensions: headerExtensions,
		Codecs:           foundCodecs,
	}
}

func (m *MediaEngine) getRTPParametersByPayloadType(payloadType PayloadType) (RTPParameters, error) {
	codec, typ, err := m.getCodecByPayload(payloadType)
	if err != nil {
		return RTPParameters{}, err
	}

	m.mu.RLock()
	defer m.mu.RUnlock()
	headerExtensions := make([]RTPHeaderExtensionParameter, 0)
	for id, e := range m.negotiatedHeaderExtensions {
		if e.isAudio && typ == RTPCodecTypeAudio || e.isVideo && typ == RTPCodecTypeVideo {
			headerExtensions = append(headerExtensions, RTPHeaderExtensionParameter{ID: id, URI: e.uri})
		}
	}

	return RTPParameters{
		HeaderExtensions: headerExtensions,
		Codecs:           []RTPCodecParameters{codec},
	}, nil
}

func payloaderForCodec(codec RTPCodecCapability) (rtp.Payloader, error) {
	switch strings.ToLower(codec.MimeType) {
	case strings.ToLower(MimeTypeH264):
		return &codecs.H264Payloader{}, nil
	case strings.ToLower(MimeTypeH265):
		return &codecs.H265Payloader{}, nil
	case strings.ToLower(MimeTypeOpus):
		return &codecs.OpusPayloader{}, nil
	case strings.ToLower(MimeTypeVP8):
		return &codecs.VP8Payloader{
			EnablePictureID: true,
		}, nil
	case strings.ToLower(MimeTypeVP9):
		return &codecs.VP9Payloader{}, nil
	case strings.ToLower(MimeTypeAV1):
		return &codecs.AV1Payloader{}, nil
	case strings.ToLower(MimeTypeG722):
		return &codecs.G722Payloader{}, nil
	case strings.ToLower(MimeTypePCMU), strings.ToLower(MimeTypePCMA):
		return &codecs.G711Payloader{}, nil
	default:
		return nil, ErrNoPayloaderForCodec
	}
}

func (m *MediaEngine) isRTXEnabled(typ RTPCodecType, directions []RTPTransceiverDirection) bool {
	for _, p := range m.getRTPParametersByKind(typ, directions).Codecs {
		if strings.EqualFold(p.MimeType, MimeTypeRTX) {
			return true
		}
	}

	return false
}

func (m *MediaEngine) isFECEnabled(typ RTPCodecType, directions []RTPTransceiverDirection) bool {
	for _, p := range m.getRTPParametersByKind(typ, directions).Codecs {
		if strings.Contains(strings.ToLower(p.MimeType), MimeTypeFlexFEC) {
			return true
		}
	}

	return false
}
