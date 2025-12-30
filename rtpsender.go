// SPDX-FileCopyrightText: 2023 The Pion community <https://pion.ly>
// SPDX-License-Identifier: MIT

//go:build !js
// +build !js

package webrtc

import (
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/pion/interceptor"
	"github.com/pion/randutil"
	"github.com/pion/rtcp"
	"github.com/pion/rtp"
	"github.com/pion/webrtc/v4/internal/util"
)

type trackEncoding struct {
	track TrackLocal // 本地轨道，用于发送媒体数据

	srtpStream *srtpWriterFuture // SRTP写入流，用于加密RTP数据包

	rtcpInterceptor interceptor.RTCPReader // RTCP拦截器，用于处理RTCP包
	streamInfo      interceptor.StreamInfo // 流信息，包含SSRC、编码参数等

	context *baseTrackLocalContext // 轨道本地上下文，包含参数和流信息

	ssrc, ssrcRTX, ssrcFEC SSRC // 主SSRC、RTX重传SSRC、FEC前向纠错SSRC
}

// RTPSender允许应用程序控制如何对给定的Track进行编码和传输到远程对等端
type RTPSender struct {
	trackEncodings []*trackEncoding // 轨道编码数组，支持多个编码（如同播）

	transport *DTLSTransport // DTLS 传输，用于加密和解密 RTP 数据

	payloadType PayloadType  // 有效载荷类型，标识媒体编码格式
	kind        RTPCodecType // 媒体编解码类型（音频/视频）

	// nolint:godox
	// TODO(sgotti) 当将来我们避免替换
	// 转发器发送者时删除此代码，因为我们可以只检查
	// 转发器协商状态
	negotiated bool // 是否已协商，表示是否已完成SDP交换

	api *API   // API引用，提供媒体引擎和拦截器的访问
	id  string // RTPSender 的唯一标识符

	rtpTransceiver *RTPTransceiver // 关联的RTPTransceiver，用于管理编码器和解码器

	mu                     sync.RWMutex
	sendCalled, stopCalled chan struct{} // 通道，用于通知/标记发送和停止操作
}

// NewRTPSender 创建一个新的 RTPSender
func (api *API) NewRTPSender(track TrackLocal, transport *DTLSTransport) (*RTPSender, error) {
	if track == nil {
		return nil, errRTPSenderTrackNil
	} else if transport == nil {
		return nil, errRTPSenderDTLSTransportNil
	}

	id, err := randutil.GenerateCryptoRandomString(32, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ")
	if err != nil {
		return nil, err
	}

	r := &RTPSender{
		transport:  transport,
		api:        api,
		sendCalled: make(chan struct{}),
		stopCalled: make(chan struct{}),
		id:         id,
		kind:       track.Kind(),
	}

	r.addEncoding(track)

	return r, nil
}

func (r *RTPSender) isNegotiated() bool {
	r.mu.RLock()
	defer r.mu.RUnlock()

	return r.negotiated
}

func (r *RTPSender) setNegotiated() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.negotiated = true
}

func (r *RTPSender) setRTPTransceiver(rtpTransceiver *RTPTransceiver) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.rtpTransceiver = rtpTransceiver
}

// Transport返回当前配置的 *DTLSTransport，如果尚未配置则返回nil
func (r *RTPSender) Transport() *DTLSTransport {
	r.mu.RLock()
	defer r.mu.RUnlock()

	return r.transport
}

// GetParameters描述发送者轨道上媒体编码和传输的当前配置
func (r *RTPSender) GetParameters() RTPSendParameters {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var encodings []RTPEncodingParameters
	for _, trackEncoding := range r.trackEncodings {
		var rid string
		if trackEncoding.track != nil {
			rid = trackEncoding.track.RID()
		}
		encodings = append(encodings, RTPEncodingParameters{
			RTPCodingParameters: RTPCodingParameters{
				RID:         rid,
				SSRC:        trackEncoding.ssrc,
				RTX:         RTPRtxParameters{SSRC: trackEncoding.ssrcRTX},
				FEC:         RTPFecParameters{SSRC: trackEncoding.ssrcFEC},
				PayloadType: r.payloadType,
			},
		})
	}
	sendParameters := RTPSendParameters{
		RTPParameters: r.api.mediaEngine.getRTPParametersByKind(
			r.kind,
			[]RTPTransceiverDirection{RTPTransceiverDirectionSendonly},
		),
		Encodings: encodings,
	}
	if r.rtpTransceiver != nil {
		sendParameters.Codecs = r.rtpTransceiver.getCodecs()
	} else {
		sendParameters.Codecs = r.api.mediaEngine.getCodecsByKind(r.kind)
	}

	return sendParameters
}

// AddEncoding向RTPSender添加编码，用于同播发送者
func (r *RTPSender) AddEncoding(track TrackLocal) error { //nolint:cyclop
	r.mu.Lock()
	defer r.mu.Unlock()

	if track == nil {
		return errRTPSenderTrackNil
	}

	if track.RID() == "" {
		return errRTPSenderRidNil
	}

	if r.hasStopped() {
		return errRTPSenderStopped
	}

	if r.hasSent() {
		return errRTPSenderSendAlreadyCalled
	}

	var refTrack TrackLocal
	if len(r.trackEncodings) != 0 {
		refTrack = r.trackEncodings[0].track
	}
	if refTrack == nil || refTrack.RID() == "" {
		return errRTPSenderNoBaseEncoding
	}

	if refTrack.ID() != track.ID() || refTrack.StreamID() != track.StreamID() || refTrack.Kind() != track.Kind() {
		return errRTPSenderBaseEncodingMismatch
	}

	for _, encoding := range r.trackEncodings {
		if encoding.track == nil {
			continue
		}

		if encoding.track.RID() == track.RID() {
			return errRTPSenderRIDCollision
		}
	}

	r.addEncoding(track)

	return nil
}

func (r *RTPSender) addEncoding(track TrackLocal) {
	trackEncoding := &trackEncoding{
		track: track,
		ssrc:  SSRC(util.RandUint32()),
	}

	if r.api.mediaEngine.isRTXEnabled(r.kind, []RTPTransceiverDirection{RTPTransceiverDirectionSendonly}) {
		trackEncoding.ssrcRTX = SSRC(util.RandUint32())
	}

	if r.api.mediaEngine.isFECEnabled(r.kind, []RTPTransceiverDirection{RTPTransceiverDirectionSendonly}) {
		trackEncoding.ssrcFEC = SSRC(util.RandUint32())
	}

	r.trackEncodings = append(r.trackEncodings, trackEncoding)
}

// Track返回RTCRtpTransceiver轨道，或nil
func (r *RTPSender) Track() TrackLocal {
	r.mu.RLock()
	defer r.mu.RUnlock()

	if len(r.trackEncodings) == 0 {
		return nil
	}

	return r.trackEncodings[0].track
}

// ReplaceTrack 用新的TrackLocal替换当前用作发送者源的轨道
// 新轨道必须是相同的媒体类型（音频、视频等），切换轨道不应需要协商
func (r *RTPSender) ReplaceTrack(track TrackLocal) error { //nolint:cyclop
	r.mu.Lock()
	defer r.mu.Unlock()

	if track != nil && r.kind != track.Kind() {
		return ErrRTPSenderNewTrackHasIncorrectKind
	}

	// cannot replace simulcast envelope
	if track != nil && len(r.trackEncodings) > 1 {
		return ErrRTPSenderNewTrackHasIncorrectEnvelope
	}

	var replacedTrack TrackLocal
	var context *baseTrackLocalContext
	for _, e := range r.trackEncodings {
		replacedTrack = e.track
		context = e.context

		if r.hasSent() && replacedTrack != nil {
			if err := replacedTrack.Unbind(context); err != nil {
				return err
			}
		}

		if !r.hasSent() || track == nil {
			e.track = track
		}
	}

	if !r.hasSent() || track == nil {
		return nil
	}

	params := r.api.mediaEngine.getRTPParametersByKind(
		track.Kind(),
		[]RTPTransceiverDirection{RTPTransceiverDirectionSendonly},
	)

	// If we reach this point in the routine, there is only 1 track encoding
	codec, err := track.Bind(&baseTrackLocalContext{
		id:              context.ID(),
		params:          params,
		ssrc:            context.SSRC(),
		ssrcRTX:         context.SSRCRetransmission(),
		ssrcFEC:         context.SSRCForwardErrorCorrection(),
		writeStream:     context.WriteStream(),
		rtcpInterceptor: context.RTCPReader(),
	})
	if err != nil {
		// Re-bind the original track
		if _, reBindErr := replacedTrack.Bind(context); reBindErr != nil {
			return reBindErr
		}

		return err
	}

	// Codec has changed
	if r.payloadType != codec.PayloadType {
		context.params.Codecs = []RTPCodecParameters{codec}
	}

	r.trackEncodings[0].track = track

	return nil
}

// Send尝试设置控制媒体发送的参数
func (r *RTPSender) Send(parameters RTPSendParameters) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	switch {
	case r.hasSent():
		return errRTPSenderSendAlreadyCalled
	case r.trackEncodings[0].track == nil:
		return errRTPSenderTrackRemoved
	}

	for idx := range r.trackEncodings {
		trackEncoding := r.trackEncodings[idx]
		srtpStream := &srtpWriterFuture{ssrc: parameters.Encodings[idx].SSRC, rtpSender: r}
		writeStream := &interceptorToTrackLocalWriter{}
		rtpParameters := r.api.mediaEngine.getRTPParametersByKind(
			trackEncoding.track.Kind(),
			[]RTPTransceiverDirection{RTPTransceiverDirectionSendonly},
		)

		trackEncoding.srtpStream = srtpStream
		trackEncoding.ssrc = parameters.Encodings[idx].SSRC
		trackEncoding.ssrcRTX = parameters.Encodings[idx].RTX.SSRC
		trackEncoding.ssrcFEC = parameters.Encodings[idx].FEC.SSRC
		trackEncoding.rtcpInterceptor = r.api.interceptor.BindRTCPReader(
			interceptor.RTCPReaderFunc(
				func(in []byte, a interceptor.Attributes) (n int, attributes interceptor.Attributes, err error) {
					n, err = trackEncoding.srtpStream.Read(in)

					return n, a, err
				},
			),
		)
		trackEncoding.context = &baseTrackLocalContext{
			id:              r.id,
			params:          rtpParameters,
			ssrc:            parameters.Encodings[idx].SSRC,
			ssrcFEC:         parameters.Encodings[idx].FEC.SSRC,
			ssrcRTX:         parameters.Encodings[idx].RTX.SSRC,
			writeStream:     writeStream,
			rtcpInterceptor: trackEncoding.rtcpInterceptor,
		}

		codec, err := trackEncoding.track.Bind(trackEncoding.context)
		if err != nil {
			return err
		}
		trackEncoding.context.params.Codecs = []RTPCodecParameters{codec}

		trackEncoding.streamInfo = *createStreamInfo(
			r.id,
			parameters.Encodings[idx].SSRC,
			parameters.Encodings[idx].RTX.SSRC,
			parameters.Encodings[idx].FEC.SSRC,
			codec.PayloadType,
			findRTXPayloadType(codec.PayloadType, rtpParameters.Codecs),
			findFECPayloadType(rtpParameters.Codecs),
			codec.RTPCodecCapability,
			parameters.HeaderExtensions,
		)

		rtpInterceptor := r.api.interceptor.BindLocalStream(
			&trackEncoding.streamInfo,
			interceptor.RTPWriterFunc(func(header *rtp.Header, payload []byte, _ interceptor.Attributes) (int, error) {
				return srtpStream.WriteRTP(header, payload)
			}),
		)

		writeStream.interceptor.Store(rtpInterceptor)
	}

	close(r.sendCalled)

	return nil
}

// Stop 不可逆地停止 RTPSender
func (r *RTPSender) Stop() error {
	r.mu.Lock()

	if stopped := r.hasStopped(); stopped {
		r.mu.Unlock()

		return nil
	}

	close(r.stopCalled)
	r.mu.Unlock()

	if !r.hasSent() {
		return nil
	}

	if err := r.ReplaceTrack(nil); err != nil {
		return err
	}

	errs := []error{}
	for _, trackEncoding := range r.trackEncodings {
		r.api.interceptor.UnbindLocalStream(&trackEncoding.streamInfo)
		if trackEncoding.srtpStream != nil {
			errs = append(errs, trackEncoding.srtpStream.Close())
		}
	}

	return util.FlattenErrs(errs)
}

// Read 读取此 RTPSender 的传入 RTCP
func (r *RTPSender) Read(b []byte) (n int, a interceptor.Attributes, err error) {
	select {
	case <-r.sendCalled:
		return r.trackEncodings[0].rtcpInterceptor.Read(b, a)
	case <-r.stopCalled:
		return 0, nil, io.ErrClosedPipe
	}
}

// ReadRTCP 是一个便利方法，它包装了 Read 并为您解包
func (r *RTPSender) ReadRTCP() ([]rtcp.Packet, interceptor.Attributes, error) {
	b := make([]byte, r.api.settingEngine.getReceiveMTU())
	i, attributes, err := r.Read(b)
	if err != nil {
		return nil, nil, err
	}

	pkts, err := rtcp.Unmarshal(b[:i])
	if err != nil {
		return nil, nil, err
	}

	return pkts, attributes, nil
}

// ReadSimulcast 为给定的 rid 读取此 RTPSender 的传入 RTCP
func (r *RTPSender) ReadSimulcast(b []byte, rid string) (n int, a interceptor.Attributes, err error) {
	select {
	case <-r.sendCalled:
		r.mu.Lock()
		for _, t := range r.trackEncodings {
			if t.track != nil && t.track.RID() == rid {
				reader := t.rtcpInterceptor
				r.mu.Unlock()

				return reader.Read(b, a)
			}
		}
		r.mu.Unlock()

		return 0, nil, fmt.Errorf("%w: %s", errRTPSenderNoTrackForRID, rid)
	case <-r.stopCalled:
		return 0, nil, io.ErrClosedPipe
	}
}

// ReadSimulcastRTCP 是一个便利方法，它包装了 ReadSimulcast 并为您解包
func (r *RTPSender) ReadSimulcastRTCP(rid string) ([]rtcp.Packet, interceptor.Attributes, error) {
	b := make([]byte, r.api.settingEngine.getReceiveMTU())
	i, attributes, err := r.ReadSimulcast(b, rid)
	if err != nil {
		return nil, nil, err
	}

	pkts, err := rtcp.Unmarshal(b[:i])

	return pkts, attributes, err
}

// SetReadDeadline 设置 Read 操作的截止时间，设置为零表示没有截止时间
func (r *RTPSender) SetReadDeadline(t time.Time) error {
	if r.trackEncodings[0].srtpStream == nil {
		return errRTPSenderSendNotCalled
	}

	return r.trackEncodings[0].srtpStream.SetReadDeadline(t)
}

// SetReadDeadlineSimulcast设置给定rid的RTCP流，在返回之前将阻塞的最大时间，0表示永远
func (r *RTPSender) SetReadDeadlineSimulcast(deadline time.Time, rid string) error {
	r.mu.RLock()
	defer r.mu.RUnlock()

	for _, t := range r.trackEncodings {
		if t.track != nil && t.track.RID() == rid {
			return t.srtpStream.SetReadDeadline(deadline)
		}
	}

	return fmt.Errorf("%w: %s", errRTPSenderNoTrackForRID, rid)
}

// hasSent检查此实例是否曾经发送过数据
func (r *RTPSender) hasSent() bool {
	select {
	case <-r.sendCalled:
		return true
	default:
		return false
	}
}

// hasStopped 检查是否已调用 stop
func (r *RTPSender) hasStopped() bool {
	select {
	case <-r.stopCalled:
		return true
	default:
		return false
	}
}

// 如果MediaEngine启用了FEC和RTX，则设置SSRC
// 如果远程不支持FEC或RTX，我们在本地禁用
func (r *RTPSender) configureRTXAndFEC() {
	r.mu.RLock()
	defer r.mu.RUnlock()

	for _, trackEncoding := range r.trackEncodings {
		if !r.api.mediaEngine.isRTXEnabled(r.kind, []RTPTransceiverDirection{RTPTransceiverDirectionSendonly}) {
			trackEncoding.ssrcRTX = SSRC(0)
		}

		if !r.api.mediaEngine.isFECEnabled(r.kind, []RTPTransceiverDirection{RTPTransceiverDirectionSendonly}) {
			trackEncoding.ssrcFEC = SSRC(0)
		}
	}
}
