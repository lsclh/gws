package gws

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

const (
	defaultReadLimit    int64 = 1 << 20
	defaultWriteTimeout       = 10 * time.Second
	sendQueueSize             = 100
	maxCloseWait              = time.Second
)

const (
	stateIdle = iota
	stateDialing
	stateRunning
	stateDraining
	stateClosed
)

// ErrNotConnected 表示连接尚未建立。
var ErrNotConnected = errors.New("gws: 未连接")

// ErrClosed 表示连接已关闭。
var ErrClosed = errors.New("gws: 已关闭")

// ErrAlreadyConnected 表示 Connect 被重复调用。
var ErrAlreadyConnected = errors.New("gws: 重复连接")

// Options 是客户端或服务端的连接配置，字段通过 Option 设置。
type Options struct {
	wsHost        *url.URL
	requestHeader http.Header
	httpWrite     http.ResponseWriter
	httpRequest   *http.Request
	checkOrigin   CheckOriginFunc
	onOpen        onOpen
	onClose       onClose
	onError       onError
	onMessage     onMessage
	serviceType   service

	readLimit       int64
	readLimitSet    bool
	pingInterval    time.Duration
	writeTimeout    time.Duration
	writeTimeoutSet bool
}

// CheckOriginFunc 在服务端握手时校验请求。返回 false 会拒绝连接。
type CheckOriginFunc func(r *http.Request, ctx *Context) bool

// Option 修改连接配置。
type Option func(e *Options)

func WithServerHttp(w http.ResponseWriter, r *http.Request) Option {
	return func(e *Options) {
		e.httpWrite = w
		e.httpRequest = r
		e.serviceType = SERVER
	}
}

// WithServerAuth 设置握手校验。不设置时使用 gorilla 的同源策略：
// 没有 Origin 头的非浏览器客户端可以连接，跨站浏览器请求会被拒绝。
func WithServerAuth(fn CheckOriginFunc) Option {
	return func(e *Options) {
		e.checkOrigin = fn
	}
}

func WithClientWsUrl(u *url.URL, requestHeader http.Header) Option {
	return func(e *Options) {
		e.wsHost = u
		e.requestHeader = requestHeader
		e.serviceType = CLIENT
	}
}

// WithReadLimit 设置单条消息的最大字节数。0 表示不限制。
func WithReadLimit(n int64) Option {
	return func(e *Options) {
		e.readLimit = n
		e.readLimitSet = true
	}
}

// WithPingInterval 打开心跳。d 是发送 Ping 的间隔，读超时为这个间隔的两倍。
// 默认关闭。不调用，或传入 0，都不会发 Ping，也没有读超时。
func WithPingInterval(d time.Duration) Option {
	return func(e *Options) {
		if d < 0 {
			d = 0
		}
		e.pingInterval = d
	}
}

// WithWriteTimeout 设置单次写入的超时时间。
func WithWriteTimeout(d time.Duration) Option {
	return func(e *Options) {
		e.writeTimeout = d
		e.writeTimeoutSet = true
	}
}

type service int

const (
	CLIENT service = 1 << 0
	SERVER service = 1 << 1
)

// Context 是单条连接的回调上下文。
type Context struct {
	client      *Client
	Ctx         context.Context
	Storage     storage
	MessageType int // 仅在 OnMessage 回调内有效，取值为 TextMessage 或 BinaryMessage
}

// ForthwithSend 立即发送文本消息。
func (ctx *Context) ForthwithSend(payload []byte) error {
	return ctx.client.ForthwithSend(payload)
}

// LaterSend 在当前已排队的立即消息之后发送文本消息。
func (ctx *Context) LaterSend(payload []byte) error {
	return ctx.client.LaterSend(payload)
}

// ForthwithSendMessage 立即发送指定类型的数据帧。
func (ctx *Context) ForthwithSendMessage(messageType int, payload []byte) error {
	return ctx.client.ForthwithSendMessage(messageType, payload)
}

// LaterSendMessage 延后发送指定类型的数据帧。
func (ctx *Context) LaterSendMessage(messageType int, payload []byte) error {
	return ctx.client.LaterSendMessage(messageType, payload)
}

// Shutdown 把已经排队的消息发完再关闭。
func (ctx *Context) Shutdown() error {
	return ctx.client.Shutdown()
}

type wsMsg struct {
	typ  int
	data []byte
}

// Client 是一条 WebSocket 连接，可以作为客户端或服务端。
type Client struct {
	options *Options
	ctx     Context

	conn   *websocket.Conn
	cancel context.CancelFunc
	base   context.Context

	mu        sync.Mutex
	cbMu      sync.Mutex
	state     int
	closeOnce sync.Once
	wg        sync.WaitGroup

	send  chan wsMsg
	later chan wsMsg

	readLimit    int64
	readWait     time.Duration
	pingInterval time.Duration
	writeTimeout time.Duration

	cond      *sync.Cond
	sending   int
	graceful  chan struct{}
	drainDone chan struct{}
	drainErr  error
}

type onOpen func(*Context)
type onClose func(*Context)
type onMessage func(*Context, []byte)
type onError func(*Context, error)

// NewWebsocket 创建一条尚未连接的 Client。
func NewWebsocket(opts ...Option) *Client {
	cf := &Options{}
	for _, fn := range opts {
		if fn != nil {
			fn(cf)
		}
	}
	c := &Client{
		options:      cf,
		readLimit:    defaultReadLimit,
		pingInterval: cf.pingInterval,
		writeTimeout: defaultWriteTimeout,
	}
	c.cond = sync.NewCond(&c.mu)
	c.ctx.client = c
	c.ctx.Ctx = context.Background()
	if cf.readLimitSet {
		c.readLimit = cf.readLimit
	}
	if cf.writeTimeoutSet && cf.writeTimeout > 0 {
		c.writeTimeout = cf.writeTimeout
	}
	return c
}

func (o *Options) validate() error {
	switch o.serviceType {
	case CLIENT:
		if o.wsHost == nil || o.wsHost.Host == "" {
			return errors.New("gws: 客户端地址为空")
		}
	case SERVER:
		if o.httpWrite == nil || o.httpRequest == nil {
			return errors.New("gws: 服务端 http 参数为空")
		}
	default:
		return errors.New("gws: 未配置客户端地址或服务端 http")
	}
	return nil
}

// Connect 建立连接并启动读写循环。失败后可以再次调用；成功或主动关闭后不能重复连接。
func (c *Client) Connect() error {
	c.mu.Lock()
	switch c.state {
	case stateRunning:
		c.mu.Unlock()
		return ErrAlreadyConnected
	case stateIdle:
	default:
		c.mu.Unlock()
		return ErrClosed
	}
	if err := c.options.validate(); err != nil {
		c.mu.Unlock()
		return err
	}
	base, cancel := context.WithCancel(context.Background())
	c.base = base
	c.cancel = cancel
	c.ctx.Ctx = base
	c.state = stateDialing
	c.mu.Unlock()

	conn, err := c.open(base)

	c.mu.Lock()
	if err != nil {
		if c.state == stateDialing {
			c.state = stateIdle
			c.base = nil
			c.cancel = nil
			c.ctx.Ctx = context.Background()
			c.mu.Unlock()
			cancel()
			return err
		}
		c.mu.Unlock()
		return err
	}
	if c.state != stateDialing {
		c.mu.Unlock()
		_ = conn.Close()
		return ErrClosed
	}

	c.conn = conn
	c.send = make(chan wsMsg, sendQueueSize)
	c.later = make(chan wsMsg, sendQueueSize)
	c.graceful = make(chan struct{}, 1)
	if c.pingInterval > 0 {
		c.readWait = c.pingInterval * 2
	} else {
		c.readWait = 0
	}
	c.prepareConn()
	c.state = stateRunning
	onOpen := c.options.onOpen
	c.wg.Add(2)
	go c.readLoop()
	go c.writeLoop()
	go c.waitClose()
	c.mu.Unlock()

	if onOpen != nil {
		if callErr := c.callUser(func() { onOpen(&c.ctx) }); callErr != nil {
			c.Close()
			return fmt.Errorf("gws: 连接回调异常: %v", callErr)
		}
	}
	return nil
}

func (c *Client) open(ctx context.Context) (*websocket.Conn, error) {
	if c.options.serviceType == CLIENT {
		conn, _, err := websocket.DefaultDialer.DialContext(ctx, c.options.wsHost.String(), c.options.requestHeader)
		return conn, err
	}
	upgrader := websocket.Upgrader{}
	if c.options.checkOrigin != nil {
		upgrader.CheckOrigin = func(r *http.Request) bool {
			return c.options.checkOrigin(r, &c.ctx)
		}
	}
	return upgrader.Upgrade(c.options.httpWrite, c.options.httpRequest, nil)
}

func (c *Client) prepareConn() {
	if c.readLimit > 0 {
		c.conn.SetReadLimit(c.readLimit)
	}
	if c.readWait <= 0 {
		return
	}
	_ = c.conn.SetReadDeadline(time.Now().Add(c.readWait))
	wait := c.readWait
	c.conn.SetPongHandler(func(string) error {
		return c.conn.SetReadDeadline(time.Now().Add(wait))
	})
}

// Close 立即关闭连接，队列里还没写出的消息直接丢掉。
// 重复调用和在回调里调用都是安全的。连接建立前调用不会产生效果。
func (c *Client) Close() {
	c.finish(true)
}

// Shutdown 先拒绝新的发送，把两个队列里已经收下的消息写完，再关闭连接。
// 写剩余消息时网络断开，返回该错误，并同样通知 OnError。
// 不要在 OnMessage、OnError、OnClose 里同步调用，需要时用 go c.Shutdown()。
func (c *Client) Shutdown() error {
	c.mu.Lock()
	switch c.state {
	case stateIdle:
		c.mu.Unlock()
		return ErrNotConnected
	case stateDialing:
		cancel := c.cancel
		c.state = stateClosed
		c.mu.Unlock()
		c.stop(nil, cancel)
		return ErrClosed
	case stateClosed:
		err := c.drainErr
		c.mu.Unlock()
		return err
	case stateDraining:
		ch := c.drainDone
		c.mu.Unlock()
		<-ch
		return c.drainResult()
	default:
		c.state = stateDraining
		ch := make(chan struct{})
		c.drainDone = ch
		c.drainErr = nil
		c.mu.Unlock()
		select {
		case c.graceful <- struct{}{}:
		default:
		}
		<-ch
		return c.drainResult()
	}
}

func (c *Client) drainResult() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.drainErr
}

func (c *Client) noteDrainErr(err error) {
	if err == nil {
		return
	}
	c.mu.Lock()
	if c.drainDone != nil && c.drainErr == nil {
		c.drainErr = err
	}
	c.mu.Unlock()
}

// finish 关闭底层连接。abandon 为 true 时，正在排空的队列不再继续写。
func (c *Client) finish(abandon bool) {
	c.mu.Lock()
	switch c.state {
	case stateIdle:
		c.mu.Unlock()
	case stateDialing:
		cancel := c.cancel
		c.state = stateClosed
		c.cond.Broadcast()
		c.mu.Unlock()
		c.stop(nil, cancel)
	case stateRunning, stateDraining:
		if abandon && c.drainDone != nil && c.drainErr == nil {
			c.drainErr = ErrClosed
		}
		conn := c.conn
		cancel := c.cancel
		c.state = stateClosed
		c.cond.Broadcast()
		c.mu.Unlock()
		c.stop(conn, cancel)
	default:
		c.cond.Broadcast()
		c.mu.Unlock()
	}
}

func (c *Client) stop(conn *websocket.Conn, cancel context.CancelFunc) {
	c.closeOnce.Do(func() {
		if cancel != nil {
			cancel()
		}
		if conn == nil {
			return
		}
		wait := c.closeWait()
		done := make(chan struct{})
		go func() {
			defer close(done)
			_ = conn.WriteControl(
				websocket.CloseMessage,
				websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""),
				time.Now().Add(wait),
			)
		}()
		timer := time.NewTimer(wait)
		defer timer.Stop()
		select {
		case <-done:
		case <-timer.C:
		}
		_ = conn.Close()
	})
}

func (c *Client) closeWait() time.Duration {
	if c.writeTimeout > 0 && c.writeTimeout < maxCloseWait {
		return c.writeTimeout
	}
	return maxCloseWait
}

func (c *Client) waitClose() {
	c.wg.Wait()
	c.mu.Lock()
	fn := c.options.onClose
	c.mu.Unlock()
	if fn == nil {
		return
	}
	_ = c.callUser(func() { fn(&c.ctx) })
}

func (c *Client) loopExit() {
	c.finish(false)
	c.wg.Done()
}

// ForthwithSend 立即发送文本消息。
func (c *Client) ForthwithSend(payload []byte) error {
	return c.enqueue(false, websocket.TextMessage, payload)
}

// LaterSend 在当前已排队的立即消息之后发送文本消息。
func (c *Client) LaterSend(payload []byte) error {
	return c.enqueue(true, websocket.TextMessage, payload)
}

// ForthwithSendMessage 立即发送文本或二进制帧。
func (c *Client) ForthwithSendMessage(messageType int, payload []byte) error {
	return c.enqueue(false, messageType, payload)
}

// LaterSendMessage 延后发送文本或二进制帧。
func (c *Client) LaterSendMessage(messageType int, payload []byte) error {
	return c.enqueue(true, messageType, payload)
}

func (c *Client) enqueue(later bool, messageType int, payload []byte) error {
	if messageType != websocket.TextMessage && messageType != websocket.BinaryMessage {
		return fmt.Errorf("gws: 不支持的消息类型 %d", messageType)
	}
	c.mu.Lock()
	if c.state != stateRunning {
		state := c.state
		c.mu.Unlock()
		if state == stateClosed || state == stateDraining {
			return ErrClosed
		}
		return ErrNotConnected
	}
	c.sending++
	ch := c.send
	if later {
		ch = c.later
	}
	done := c.base.Done()
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		c.sending--
		c.cond.Broadcast()
		c.mu.Unlock()
	}()

	msg := wsMsg{typ: messageType, data: cloneBytes(payload)}
	select {
	case <-done:
		return ErrClosed
	case ch <- msg:
		return nil
	}
}

func cloneBytes(p []byte) []byte {
	if len(p) == 0 {
		return nil
	}
	b := make([]byte, len(p))
	copy(b, p)
	return b
}

// OnOpen 设置连接成功回调，应在 Connect 之前调用。
func (c *Client) OnOpen(fn onOpen) {
	c.mu.Lock()
	c.options.onOpen = fn
	c.mu.Unlock()
}

// OnClose 设置连接结束回调。回调返回后不会再有读写。
func (c *Client) OnClose(fn onClose) {
	c.mu.Lock()
	c.options.onClose = fn
	c.mu.Unlock()
}

// OnError 设置错误回调。正常关闭不会触发。
func (c *Client) OnError(fn onError) {
	c.mu.Lock()
	c.options.onError = fn
	c.mu.Unlock()
}

// OnMessage 设置消息回调。不设置时仍会读取连接，以便处理关闭和心跳。
func (c *Client) OnMessage(fn onMessage) {
	c.mu.Lock()
	c.options.onMessage = fn
	c.mu.Unlock()
}

func (c *Client) readLoop() {
	defer c.loopExit()
	defer c.recoverAs("读取")
	for {
		if c.readWait > 0 {
			_ = c.conn.SetReadDeadline(time.Now().Add(c.readWait))
		}
		mt, payload, err := c.conn.ReadMessage()
		if err != nil {
			report := c.shouldReport(err)
			c.Close()
			if report {
				c.reportError(err)
			}
			return
		}
		c.mu.Lock()
		fn := c.options.onMessage
		c.mu.Unlock()
		if fn == nil {
			continue
		}
		go c.handleMessage(fn, mt, payload)
	}
}

// handleMessage 在独立协程里执行业务回调。回调 panic 只通知 OnError，不关闭连接，读循环继续。
func (c *Client) handleMessage(fn onMessage, mt int, payload []byte) {
	defer func() {
		if r := recover(); r != nil {
			c.reportError(fmt.Errorf("gws: 消息回调异常: %v", r))
		}
	}()
	c.cbMu.Lock()
	defer c.cbMu.Unlock()
	c.ctx.MessageType = mt
	fn(&c.ctx, payload)
}

func (c *Client) writeLoop() {
	defer c.wakeDrain()
	defer c.loopExit()
	defer c.recoverAs("发送")

	var pingC <-chan time.Time
	if c.pingInterval > 0 {
		ticker := time.NewTicker(c.pingInterval)
		defer ticker.Stop()
		pingC = ticker.C
	}
	for {
		select {
		case <-c.base.Done():
			return
		case <-c.graceful:
			c.drainAll()
			return
		case msg := <-c.send:
			if !c.writeOne(msg) {
				return
			}
		case msg := <-c.later:
			if !c.writeLater(msg) {
				return
			}
		case <-pingC:
			if err := c.conn.WriteControl(websocket.PingMessage, nil, time.Now().Add(c.writeTimeout)); err != nil {
				c.fail(err)
				return
			}
		}
	}
}

// drainAll 写完调用 Shutdown 时已经排队的消息。写入失败或被 Close 打断时返回。
func (c *Client) drainAll() {
	for {
		msg, ok := c.popQueued()
		if ok {
			if !c.writeOne(msg) {
				return
			}
			continue
		}
		c.mu.Lock()
		for c.state == stateDraining && c.sending > 0 && len(c.send) == 0 && len(c.later) == 0 {
			c.cond.Wait()
		}
		state := c.state
		pending := len(c.send) > 0 || len(c.later) > 0 || c.sending > 0
		c.mu.Unlock()
		if state != stateDraining || !pending {
			return
		}
	}
}

func (c *Client) popQueued() (wsMsg, bool) {
	select {
	case msg := <-c.send:
		return msg, true
	default:
	}
	select {
	case msg := <-c.send:
		return msg, true
	case msg := <-c.later:
		return msg, true
	default:
		return wsMsg{}, false
	}
}

func (c *Client) wakeDrain() {
	c.mu.Lock()
	ch := c.drainDone
	c.drainDone = nil
	c.mu.Unlock()
	if ch != nil {
		close(ch)
	}
}

func (c *Client) writeLater(msg wsMsg) bool {
	// 只让出已经排在立即队列里的消息，避免新的立即消息把这条延后消息饿死。
	for i := 0; i < sendQueueSize; i++ {
		select {
		case <-c.base.Done():
			return false
		case first := <-c.send:
			if !c.writeOne(first) {
				return false
			}
		default:
			return c.writeOne(msg)
		}
	}
	return c.writeOne(msg)
}

func (c *Client) writeOne(msg wsMsg) bool {
	if err := c.conn.SetWriteDeadline(time.Now().Add(c.writeTimeout)); err != nil {
		c.noteDrainErr(err)
		c.fail(err)
		return false
	}
	if err := c.conn.WriteMessage(msg.typ, msg.data); err != nil {
		c.noteDrainErr(err)
		c.fail(err)
		return false
	}
	return true
}

func (c *Client) fail(err error) {
	report := c.shouldReport(err)
	c.Close()
	if report {
		c.reportError(err)
	}
}

func (c *Client) recoverAs(name string) {
	if r := recover(); r != nil {
		c.Close()
		c.reportError(fmt.Errorf("gws: %s协程异常: %v", name, r))
	}
}

func (c *Client) shouldReport(err error) bool {
	if err == nil || c.isClosed() || isBenignClose(err) {
		return false
	}
	return true
}

func (c *Client) isClosed() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.state == stateClosed
}

func isBenignClose(err error) bool {
	if errors.Is(err, context.Canceled) || errors.Is(err, net.ErrClosed) || errors.Is(err, websocket.ErrCloseSent) {
		return true
	}
	return websocket.IsCloseError(err,
		websocket.CloseNormalClosure,
		websocket.CloseGoingAway,
	)
}

func (c *Client) reportError(err error) {
	if err == nil {
		return
	}
	c.mu.Lock()
	fn := c.options.onError
	c.mu.Unlock()
	if fn == nil {
		return
	}
	_ = c.callUser(func() { fn(&c.ctx, err) })
}

func (c *Client) callUser(fn func()) (err error) {
	c.cbMu.Lock()
	defer c.cbMu.Unlock()
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("%v", r)
		}
	}()
	fn()
	return nil
}
