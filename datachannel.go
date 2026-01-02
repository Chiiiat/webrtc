// SPDX-FileCopyrightText: 2023 The Pion community <https://pion.ly>
// SPDX-License-Identifier: MIT

//go:build !js
// +build !js

package webrtc

import (
	"errors"
	"fmt"
	"io"
	"sync"
	"sync/atomic"
	"time"

	"github.com/pion/datachannel"
	"github.com/pion/logging"
	"github.com/pion/webrtc/v4/pkg/rtcerr"
)

var errSCTPNotEstablished = errors.New("SCTP not established")

// DataChannel表示一个WebRTC数据通道
// DataChannel interface 表示一个网络通道
// 可用于任意数据的双向对等传输
type DataChannel struct {
	mu sync.RWMutex

	statsID                    string
	label                      string
	ordered                    bool
	maxPacketLifeTime          *uint16
	maxRetransmits             *uint16
	protocol                   string
	negotiated                 bool
	id                         *uint16
	readyState                 atomic.Value // DataChannelState
	bufferedAmountLowThreshold uint64
	detachCalled               bool
	readLoopActive             chan struct{}
	isGracefulClosed           bool

	// binaryType表示属性在获取时必须返回上次设置的值。在设置时，如果新值是字符串
	// "blob"或字符串"arraybuffer"，则将IDL属性设置为该
	// 新值。否则，抛出SyntaxError。创建DataChannel对象时
	// binaryType属性必须初始化为字符串
	// "blob"。此属性控制二进制数据如何暴露给脚本
	// binaryType                 string

	onMessageHandler    func(DataChannelMessage)
	openHandlerOnce     sync.Once
	onOpenHandler       func()
	dialHandlerOnce     sync.Once
	onDialHandler       func()
	onCloseHandler      func()
	onBufferedAmountLow func()
	onErrorHandler      func(error)

	sctpTransport *SCTPTransport
	dataChannel   *datachannel.DataChannel

	// A reference to the associated api object used by this datachannel
	api *API
	log logging.LeveledLogger
}

// NewDataChannel创建一个新的数据通道
// 此构造函数是ORTC API的一部分，它不
// 应与基本WebRTC API一起使用
func (api *API) NewDataChannel(transport *SCTPTransport, params *DataChannelParameters) (*DataChannel, error) {
	d, err := api.newDataChannel(params, nil, api.settingEngine.LoggerFactory.NewLogger("ortc"))
	if err != nil {
		return nil, err
	}

	err = d.open(transport)
	if err != nil {
		return nil, err
	}

	return d, nil
}

// newDataChannel是数据通道的内部构造函数，用于
// 在网络设置之前创建DataChannel对象
func (api *API) newDataChannel(
	params *DataChannelParameters,
	sctpTransport *SCTPTransport,
	log logging.LeveledLogger,
) (*DataChannel, error) {
	// https://w3c.github.io/webrtc-pc/#peer-to-peer-data-api (Step #5)
	if len(params.Label) > 65535 {
		return nil, &rtcerr.TypeError{Err: ErrStringSizeLimit}
	}

	dataChannel := &DataChannel{
		sctpTransport:     sctpTransport,
		statsID:           fmt.Sprintf("DataChannel-%d", time.Now().UnixNano()),
		label:             params.Label,
		protocol:          params.Protocol,
		negotiated:        params.Negotiated,
		id:                params.ID,
		ordered:           params.Ordered,
		maxPacketLifeTime: params.MaxPacketLifeTime,
		maxRetransmits:    params.MaxRetransmits,
		api:               api,
		log:               log,
	}

	dataChannel.setReadyState(DataChannelStateConnecting)

	return dataChannel, nil
}

// open在SCTP传输上打开数据通道
func (d *DataChannel) open(sctpTransport *SCTPTransport) error { //nolint:cyclop
	association := sctpTransport.association()
	if association == nil {
		return errSCTPNotEstablished
	}

	d.mu.Lock()
	if d.sctpTransport != nil { // already open
		d.mu.Unlock()

		return nil
	}
	d.sctpTransport = sctpTransport
	var channelType datachannel.ChannelType
	var reliabilityParameter uint32

	switch {
	case d.maxPacketLifeTime == nil && d.maxRetransmits == nil:
		if d.ordered {
			channelType = datachannel.ChannelTypeReliable
		} else {
			channelType = datachannel.ChannelTypeReliableUnordered
		}

	case d.maxRetransmits != nil:
		reliabilityParameter = uint32(*d.maxRetransmits)
		if d.ordered {
			channelType = datachannel.ChannelTypePartialReliableRexmit
		} else {
			channelType = datachannel.ChannelTypePartialReliableRexmitUnordered
		}
	default:
		reliabilityParameter = uint32(*d.maxPacketLifeTime)
		if d.ordered {
			channelType = datachannel.ChannelTypePartialReliableTimed
		} else {
			channelType = datachannel.ChannelTypePartialReliableTimedUnordered
		}
	}

	cfg := &datachannel.Config{
		ChannelType:          channelType,
		Priority:             datachannel.ChannelPriorityNormal,
		ReliabilityParameter: reliabilityParameter,
		Label:                d.label,
		Protocol:             d.protocol,
		Negotiated:           d.negotiated,
		LoggerFactory:        d.api.settingEngine.LoggerFactory,
	}

	if d.id == nil {
		// avoid holding lock when generating ID, since id generation locks
		d.mu.Unlock()
		var dcID *uint16
		err := d.sctpTransport.generateAndSetDataChannelID(d.sctpTransport.dtlsTransport.role(), &dcID)
		if err != nil {
			return err
		}
		d.mu.Lock()
		d.id = dcID
	}
	dc, err := datachannel.Dial(association, *d.id, cfg)
	if err != nil {
		d.mu.Unlock()

		return err
	}

	// bufferedAmountLowThreshold and onBufferedAmountLow might be set earlier
	dc.SetBufferedAmountLowThreshold(d.bufferedAmountLowThreshold)
	dc.OnBufferedAmountLow(d.onBufferedAmountLow)
	d.mu.Unlock()

	d.onDial()
	d.handleOpen(dc, false, d.negotiated)

	return nil
}

// Transport返回DataChannel正在其上发送的SCTPTransport实例
func (d *DataChannel) Transport() *SCTPTransport {
	d.mu.RLock()
	defer d.mu.RUnlock()

	return d.sctpTransport
}

// onOpen完成后检查用户是否调用了detach
// 如果调用被遗漏则提供错误消息
func (d *DataChannel) checkDetachAfterOpen() {
	d.mu.RLock()
	defer d.mu.RUnlock()

	if d.api.settingEngine.detach.DataChannels && !d.detachCalled {
		d.log.Warn("webrtc.DetachDataChannels() enabled but didn't Detach, call Detach from OnOpen")
	}
}

// OnOpen设置一个事件处理器，当
// 底层数据传输已建立（或重新建立）时调用
func (d *DataChannel) OnOpen(f func()) {
	d.mu.Lock()
	d.openHandlerOnce = sync.Once{}
	d.onOpenHandler = f
	d.mu.Unlock()

	if d.ReadyState() == DataChannelStateOpen {
		// If the data channel is already open, call the handler immediately.
		go d.openHandlerOnce.Do(func() {
			f()
			d.checkDetachAfterOpen()
		})
	}
}

func (d *DataChannel) onOpen() {
	d.mu.RLock()
	handler := d.onOpenHandler
	if d.isGracefulClosed {
		d.mu.RUnlock()

		return
	}
	d.mu.RUnlock()

	if handler != nil {
		go d.openHandlerOnce.Do(func() {
			handler()
			d.checkDetachAfterOpen()
		})
	}
}

// OnDial设置一个事件处理器，当
// 对等方已拨号但在该对等方响应之前调用
func (d *DataChannel) OnDial(f func()) {
	d.mu.Lock()
	d.dialHandlerOnce = sync.Once{}
	d.onDialHandler = f
	d.mu.Unlock()

	if d.ReadyState() == DataChannelStateOpen {
		// If the data channel is already open, call the handler immediately.
		go d.dialHandlerOnce.Do(f)
	}
}

func (d *DataChannel) onDial() {
	d.mu.RLock()
	handler := d.onDialHandler
	if d.isGracefulClosed {
		d.mu.RUnlock()

		return
	}
	d.mu.RUnlock()

	if handler != nil {
		go d.dialHandlerOnce.Do(handler)
	}
}

// OnClose设置一个事件处理器，当
// 底层数据传输已关闭时调用
// 注意：由于向后兼容性，有可能
// OnClose会被调用即使使用了GracefulClose
// 如果这对您是这样，您可以在GracefulClose之前
// 注销OnClose
func (d *DataChannel) OnClose(f func()) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.onCloseHandler = f
}

func (d *DataChannel) onClose() {
	d.mu.RLock()
	handler := d.onCloseHandler
	d.mu.RUnlock()

	if handler != nil {
		go handler()
	}
}

// OnMessage设置一个事件处理器，当从远程对等方通过sctp传输
// 接收到二进制消息时调用
// OnMessage当前可接收最多16384字节
// 大小的消息。如果您想使用更大的
// 消息大小请查看detach API。请注意浏览器对更大消息
// 的支持也有限
func (d *DataChannel) OnMessage(f func(msg DataChannelMessage)) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.onMessageHandler = f
}

func (d *DataChannel) onMessage(msg DataChannelMessage) {
	d.mu.RLock()
	handler := d.onMessageHandler
	if d.isGracefulClosed {
		d.mu.RUnlock()

		return
	}
	d.mu.RUnlock()

	if handler == nil {
		return
	}
	handler(msg)
}

func (d *DataChannel) handleOpen(dc *datachannel.DataChannel, isRemote, isAlreadyNegotiated bool) {
	d.mu.Lock()
	if d.isGracefulClosed { // The channel was closed during the connecting state
		d.mu.Unlock()
		if err := dc.Close(); err != nil {
			d.log.Errorf("Failed to close DataChannel that was closed during connecting state %v", err.Error())
		}
		d.onClose()

		return
	}
	d.dataChannel = dc
	bufferedAmountLowThreshold := d.bufferedAmountLowThreshold
	onBufferedAmountLow := d.onBufferedAmountLow
	d.mu.Unlock()
	d.setReadyState(DataChannelStateOpen)

	// Fire the OnOpen handler immediately not using pion/datachannel
	// * detached datachannels have no read loop, the user needs to read and query themselves
	// * remote datachannels should fire OnOpened. This isn't spec compliant, but we can't break behavior yet
	// * already negotiated datachannels should fire OnOpened
	if d.api.settingEngine.detach.DataChannels || isRemote || isAlreadyNegotiated {
		// bufferedAmountLowThreshold and onBufferedAmountLow might be set earlier
		d.dataChannel.SetBufferedAmountLowThreshold(bufferedAmountLowThreshold)
		d.dataChannel.OnBufferedAmountLow(onBufferedAmountLow)
		d.onOpen()
	} else {
		dc.OnOpen(func() {
			d.onOpen()
		})
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	if d.isGracefulClosed {
		return
	}

	if !d.api.settingEngine.detach.DataChannels {
		d.readLoopActive = make(chan struct{})
		go d.readLoop()
	}
}

// OnError设置一个事件处理器，当
// 底层数据传输无法读取时调用
func (d *DataChannel) OnError(f func(err error)) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.onErrorHandler = f
}

func (d *DataChannel) onError(err error) {
	d.mu.RLock()
	handler := d.onErrorHandler
	if d.isGracefulClosed {
		d.mu.RUnlock()

		return
	}
	d.mu.RUnlock()

	if handler != nil {
		go handler(err)
	}
}

func (d *DataChannel) readLoop() {
	defer func() {
		d.mu.Lock()
		readLoopActive := d.readLoopActive
		d.mu.Unlock()
		defer close(readLoopActive)
	}()

	buffer := make([]byte, sctpMaxMessageSizeUnsetValue)
	for {
		n, isString, err := d.dataChannel.ReadDataChannel(buffer)
		if err != nil {
			if errors.Is(err, io.ErrShortBuffer) {
				if int64(n) < int64(d.api.settingEngine.getSCTPMaxMessageSize()) {
					buffer = append(buffer, make([]byte, len(buffer))...) // nolint

					continue
				}

				d.log.Errorf(
					"Incoming DataChannel message larger then Max Message size %v",
					d.api.settingEngine.getSCTPMaxMessageSize(),
				)
			}

			d.setReadyState(DataChannelStateClosed)
			if !errors.Is(err, io.EOF) {
				d.onError(err)
			}
			d.onClose()

			return
		}

		d.onMessage(DataChannelMessage{
			Data:     append([]byte{}, buffer[:n]...),
			IsString: isString,
		})
	}
}

// Send 向DataChannel对等方发送二进制消息
func (d *DataChannel) Send(data []byte) error {
	err := d.ensureOpen()
	if err != nil {
		return err
	}

	_, err = d.dataChannel.WriteDataChannel(data, false)

	return err
}

// SendText 向DataChannel对等方发送文本消息
func (d *DataChannel) SendText(s string) error {
	err := d.ensureOpen()
	if err != nil {
		return err
	}

	_, err = d.dataChannel.WriteDataChannel([]byte(s), true)

	return err
}

func (d *DataChannel) ensureOpen() error {
	d.mu.RLock()
	defer d.mu.RUnlock()
	if d.ReadyState() != DataChannelStateOpen {
		return io.ErrClosedPipe
	}

	return nil
}

// Detach 允许您分离底层数据通道
// 这提供了一个惯用的API来工作
// （`io.ReadWriteCloser`及其`.Read()`和`.Write()`方法
// 而不是`.Send()`和`.OnMessage`
// 然而它禁用了OnMessage回调
// 调用Detach之前您必须通过调用
// webrtc.DetachDataChannels()启用此行为。组合分离和正常数据通道
// 不被支持
// 请参阅data-channels-detach示例和
// pion/datachannel文档了解正确处理
// 生成的DataChannel对象的方法
func (d *DataChannel) Detach() (datachannel.ReadWriteCloser, error) {
	return d.DetachWithDeadline()
}

// DetachWithDeadline允许您分离底层数据通道
// 它与Detach相同但返回一个ReadWriteCloserDeadliner
func (d *DataChannel) DetachWithDeadline() (datachannel.ReadWriteCloserDeadliner, error) {
	d.mu.Lock()

	if !d.api.settingEngine.detach.DataChannels {
		d.mu.Unlock()

		return nil, errDetachNotEnabled
	}

	if d.dataChannel == nil {
		d.mu.Unlock()

		return nil, errDetachBeforeOpened
	}

	d.detachCalled = true

	dataChannel := d.dataChannel
	d.mu.Unlock()

	// Remove the reference from SCTPTransport so that the datachannel
	// can be garbage collected on close
	d.sctpTransport.lock.Lock()
	n := len(d.sctpTransport.dataChannels)
	j := 0
	for i := 0; i < n; i++ {
		if d == d.sctpTransport.dataChannels[i] {
			continue
		}
		d.sctpTransport.dataChannels[j] = d.sctpTransport.dataChannels[i]
		j++
	}
	for i := j; i < n; i++ {
		d.sctpTransport.dataChannels[i] = nil
	}
	d.sctpTransport.dataChannels = d.sctpTransport.dataChannels[:j]
	d.sctpTransport.lock.Unlock()

	return dataChannel, nil
}

// Close关闭DataChannel，无论
// DataChannel对象是由此对等方还是远程对等方创建都可以调用
func (d *DataChannel) Close() error {
	return d.close(false)
}

// GracefulClose 关闭DataChannel，无论
// DataChannel对象是由此对等方还是远程对等方创建都可以调用
// 在其自己的goroutine中，它还会等待它启动的任何goroutine完成，这仅在
// DataChannel回调之外调用是安全的或如果在回调中
func (d *DataChannel) GracefulClose() error {
	return d.close(true)
}

// 通常close只停止写入发生，所以graceful=true
// 将基于底层SCTP关联等待读取完成
// 关闭或来自另一侧的SCTP重置流。这在拆除PeerConnection后
// 用graceful=true调用是安全的但不一定
// 在此之前。例如，如果您使用了vnet并在关闭DataChannel前
// 丢弃了所有数据包，您可能永远不会看到重置流
func (d *DataChannel) close(shouldGracefullyClose bool) error {
	d.mu.Lock()
	d.isGracefulClosed = true
	readLoopActive := d.readLoopActive
	if shouldGracefullyClose && readLoopActive != nil {
		defer func() {
			<-readLoopActive
		}()
	}
	haveSctpTransport := d.dataChannel != nil
	d.mu.Unlock()

	if d.ReadyState() == DataChannelStateClosed {
		return nil
	}

	d.setReadyState(DataChannelStateClosing)
	if !haveSctpTransport {
		return nil
	}

	return d.dataChannel.Close()
}

// Label表示可用于区分此
// DataChannel对象与其他DataChannel对象的标签
// 脚本被允许创建具有相同标签的多个DataChannel对象
func (d *DataChannel) Label() string {
	d.mu.RLock()
	defer d.mu.RUnlock()

	return d.label
}

// Ordered如果DataChannel是有序的则返回true，如果
// 允许无序交付则返回false
func (d *DataChannel) Ordered() bool {
	d.mu.RLock()
	defer d.mu.RUnlock()

	return d.ordered
}

// MaxPacketLifeTime表示时间窗口的长度（毫秒）在
// 不可靠模式下可能发生传输和重传
func (d *DataChannel) MaxPacketLifeTime() *uint16 {
	d.mu.RLock()
	defer d.mu.RUnlock()

	return d.maxPacketLifeTime
}

// MaxRetransmits表示在
// 不可靠模式下尝试的最大重传次数
func (d *DataChannel) MaxRetransmits() *uint16 {
	d.mu.RLock()
	defer d.mu.RUnlock()

	return d.maxRetransmits
}

// Protocol表示与此
// DataChannel一起使用的子协议名称
func (d *DataChannel) Protocol() string {
	d.mu.RLock()
	defer d.mu.RUnlock()

	return d.protocol
}

// Negotiated表示此DataChannel是否由
// 应用程序协商（true）或没有（false）
func (d *DataChannel) Negotiated() bool {
	d.mu.RLock()
	defer d.mu.RUnlock()

	return d.negotiated
}

// ID表示此DataChannel的ID，值最初是
// null，如果在通道创建时未提供ID，且SCTP传输的DTLS角色尚未
// 协商则将返回此值
// 否则，它将返回由脚本选择或生成的ID，ID设置为非null
// 值后将不会更改
func (d *DataChannel) ID() *uint16 {
	d.mu.RLock()
	defer d.mu.RUnlock()

	return d.id
}

// ReadyState表示DataChannel对象的状态
func (d *DataChannel) ReadyState() DataChannelState {
	if v, ok := d.readyState.Load().(DataChannelState); ok {
		return v
	}

	return DataChannelState(0)
}

// BufferedAmount表示使用send()排队的应用程序数据字节数
// （UTF-8文本和二进制数据）
// 即使数据传输可以并行发生，返回值
// 在当前任务交还给事件循环之前不得减少
// 以防止竞态条件。该值不包括协议
// 产生的帧开销或操作系统或网络硬件
// 执行的缓冲。只要ReadyState为
// 开放，BufferedAmount槽的值只会随每次调用send()方法而
// 增加；然而，通道
// 关闭后BufferedAmount不会重置为零
func (d *DataChannel) BufferedAmount() uint64 {
	d.mu.RLock()
	defer d.mu.RUnlock()

	if d.dataChannel == nil {
		return 0
	}

	return d.dataChannel.BufferedAmount()
}

// BufferedAmountLowThreshold表示
// bufferedAmount被认为较低的阈值
// 当bufferedAmount从
// 高于此阈值减少到等于或低于它时，bufferedamountlow
// 事件触发
// 每个新DataChannel上的BufferedAmountLowThreshold最初为零，但应用程序可以在任何时间
// 更改其值
// 阈值默认设置为0
func (d *DataChannel) BufferedAmountLowThreshold() uint64 {
	d.mu.RLock()
	defer d.mu.RUnlock()

	if d.dataChannel == nil {
		return d.bufferedAmountLowThreshold
	}

	return d.dataChannel.BufferedAmountLowThreshold()
}

// SetBufferedAmountLowThreshold用于更新阈值
// 参见BufferedAmountLowThreshold()
func (d *DataChannel) SetBufferedAmountLowThreshold(th uint64) {
	d.mu.Lock()
	defer d.mu.Unlock()

	d.bufferedAmountLowThreshold = th

	if d.dataChannel != nil {
		d.dataChannel.SetBufferedAmountLowThreshold(th)
	}
}

// OnBufferedAmountLow设置一个事件处理器当
// 出站数据字节数低于或等于
// BufferedAmountLowThreshold时调用
func (d *DataChannel) OnBufferedAmountLow(f func()) {
	d.mu.Lock()
	defer d.mu.Unlock()

	onBufferedAmountLow := d.makeBufferedAmountLowHandler(f)
	d.onBufferedAmountLow = onBufferedAmountLow

	if d.dataChannel != nil {
		d.dataChannel.OnBufferedAmountLow(onBufferedAmountLow)
	}
}

func (d *DataChannel) makeBufferedAmountLowHandler(f func()) func() {
	return func() {
		go func() {
			if d.ReadyState() != DataChannelStateOpen {
				return
			}

			f()
		}()
	}
}

func (d *DataChannel) getStatsID() string {
	d.mu.Lock()
	defer d.mu.Unlock()

	return d.statsID
}

func (d *DataChannel) collectStats(collector *statsReportCollector) {
	collector.Collecting()

	d.mu.Lock()
	defer d.mu.Unlock()

	stats := DataChannelStats{
		Timestamp: statsTimestampNow(),
		Type:      StatsTypeDataChannel,
		ID:        d.statsID,
		Label:     d.label,
		Protocol:  d.protocol,
		// TransportID string `json:"transportId"`
		State: d.ReadyState(),
	}

	if d.id != nil {
		stats.DataChannelIdentifier = int32(*d.id)
	}

	if d.dataChannel != nil {
		stats.MessagesSent = d.dataChannel.MessagesSent()
		stats.BytesSent = d.dataChannel.BytesSent()
		stats.MessagesReceived = d.dataChannel.MessagesReceived()
		stats.BytesReceived = d.dataChannel.BytesReceived()
	}

	collector.Collect(stats.ID, stats)
}

func (d *DataChannel) setReadyState(r DataChannelState) {
	d.readyState.Store(r)
}
