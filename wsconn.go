package gws

import (
	"context"
	"fmt"
	"github.com/gorilla/websocket"
	"net"
	"net/http"
	"net/url"
	"sync"
	"time"
)

// 配置文件
type Options struct {

	//作为客户端使用
	wsHost *url.URL

	//作为客户端连接使用
	requestHeader http.Header

	//http写对象 作为服务端使用
	httpWrite http.ResponseWriter

	//http请求对象 作为服务端使用
	httpRequest *http.Request

	//验证连接权限
	checkOrigin CheckOriginFunc

	//连接成功回调
	onOpen onOpen

	//关闭回调
	onClose onClose

	//错误回调
	onError onError

	//接收到消息回调
	onMessage onMessage

	//连接类型
	serviceType service
}

type CheckOriginFunc func(r *http.Request, ctx *Context) bool

type Option func(e *Options)

func WithServerHttp(w http.ResponseWriter, r *http.Request) Option {
	return func(e *Options) {
		e.httpWrite = w
		e.httpRequest = r
		e.serviceType = SERVER
	}
}

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

/*************************************************WS链接可操作函数start***************************************************/

// 获取ws对象
func NewWebsocket(opts ...Option) (cl *client) {
	cf := &Options{}
	for _, fn := range opts {
		fn(cf)
	}
	if cf.checkOrigin == nil {
		cf.checkOrigin = func(r *http.Request, ctx *Context) bool {
			return true
		}
	}
	cl = &client{
		options: cf,
		isClose: true,
		ctx: Context{
			Ctx: context.Background(),
		},
	}
	return
}

// 开始连接
func (c *client) Connect() error {
	if c.options.serviceType == CLIENT {
		conn, _, err := websocket.DefaultDialer.Dial(c.options.wsHost.String(), c.options.requestHeader)
		if err != nil {
			return err
		}
		c.ctx.conn = conn
	} else {
		conn, err := (&websocket.Upgrader{CheckOrigin: func(r *http.Request) bool {
			return c.options.checkOrigin(r, &c.ctx)

		}}).Upgrade(c.options.httpWrite, c.options.httpRequest, nil)
		if err != nil {
			http.NotFound(c.options.httpWrite, c.options.httpRequest)
			return err
		}
		c.ctx.conn = conn
	}
	return c.init()
}

// 立即推送 -- 需要推送数量过多时 优先推送的数据
func (c *client) ForthwithSend(payload []byte) error {
	return c.ctx.ForthwithSend(payload)
}

// 稍后推送 -- 需要推送数量过多时 延后推送的数据
func (c *client) LaterSend(payload []byte) error {
	return c.ctx.LaterSend(payload)
}

// 断开连接
func (c *client) Close() {
	c.close()
}

/*************************************************WS链接可操作函数end***************************************************/

/*************************************************WS链接回调函数start***************************************************/

// 连接成功事件
func (c *client) OnOpen(fn onOpen) {
	c.options.onOpen = fn
}

// 断开连接
func (c *client) OnClose(fn onClose) {
	c.options.onClose = fn
}

// 连接期间发生错误
func (c *client) OnError(fn onError) {
	c.options.onError = fn
}

func (c *client) OnMessage(fn onMessage) {
	c.options.onMessage = fn
}

/*************************************************WS链接回调函数end***************************************************/

/*************************************************WS链接内部逻辑start***************************************************/

// 代码依赖项
type service int

const (
	CLIENT service = 1 << 0 //标记为客户端
	SERVER service = 1 << 1 //标记为服务端
)

type Context struct {
	conn           *websocket.Conn
	sendChan       chan []byte //主发送消息
	secondSendChan chan []byte //次发送消息
	Ctx            context.Context
	Storage        storage
}

type storage struct {
	mu   sync.RWMutex
	Keys map[string]any
}

func (c *Context) ForthwithSend(payload []byte) (err error) {
	defer func() {
		if panicErr := recover(); panicErr != nil {
			err = fmt.Errorf("写异常: %v", panicErr)
		}
	}()
	c.sendChan <- payload
	return
}

// 稍后推送 -- 需要推送数量过多时 延后推送的数据
func (c *Context) LaterSend(payload []byte) (err error) {
	defer func() {
		if panicErr := recover(); panicErr != nil {
			err = fmt.Errorf("写异常: %v", panicErr)
		}
	}()
	c.secondSendChan <- payload
	return
}

/************************************/
/******** METADATA MANAGEMENT********/
/************************************/

// Set is used to store a new key/value pair exclusively for this context.
// It also lazy initializes  c.Keys if it was not used previously.
func (c *storage) Set(key string, value any) {
	c.mu.Lock()
	if c.Keys == nil {
		c.Keys = make(map[string]any)
	}

	c.Keys[key] = value
	c.mu.Unlock()
}

// Get returns the value for the given key, ie: (value, true).
// If the value does not exists it returns (nil, false)
func (c *storage) Get(key string) (value any, exists bool) {
	c.mu.RLock()
	value, exists = c.Keys[key]
	c.mu.RUnlock()
	return
}

// GetString returns the value associated with the key as a string.
func (c *storage) GetString(key string) (s string) {
	if val, ok := c.Get(key); ok && val != nil {
		s, _ = val.(string)
	}
	return
}

// GetBool returns the value associated with the key as a boolean.
func (c *storage) GetBool(key string) (b bool) {
	if val, ok := c.Get(key); ok && val != nil {
		b, _ = val.(bool)
	}
	return
}

// GetInt returns the value associated with the key as an integer.
func (c *storage) GetInt(key string) (i int) {
	if val, ok := c.Get(key); ok && val != nil {
		i, _ = val.(int)
	}
	return
}

// GetInt64 returns the value associated with the key as an integer.
func (c *storage) GetInt64(key string) (i64 int64) {
	if val, ok := c.Get(key); ok && val != nil {
		i64, _ = val.(int64)
	}
	return
}

// GetUint returns the value associated with the key as an unsigned integer.
func (c *storage) GetUint(key string) (ui uint) {
	if val, ok := c.Get(key); ok && val != nil {
		ui, _ = val.(uint)
	}
	return
}

// GetUint64 returns the value associated with the key as an unsigned integer.
func (c *storage) GetUint64(key string) (ui64 uint64) {
	if val, ok := c.Get(key); ok && val != nil {
		ui64, _ = val.(uint64)
	}
	return
}

// GetFloat64 returns the value associated with the key as a float64.
func (c *storage) GetFloat64(key string) (f64 float64) {
	if val, ok := c.Get(key); ok && val != nil {
		f64, _ = val.(float64)
	}
	return
}

// GetTime returns the value associated with the key as time.
func (c *storage) GetTime(key string) (t time.Time) {
	if val, ok := c.Get(key); ok && val != nil {
		t, _ = val.(time.Time)
	}
	return
}

// GetDuration returns the value associated with the key as a duration.
func (c *storage) GetDuration(key string) (d time.Duration) {
	if val, ok := c.Get(key); ok && val != nil {
		d, _ = val.(time.Duration)
	}
	return
}

// GetStringSlice returns the value associated with the key as a slice of strings.
func (c *storage) GetStringSlice(key string) (ss []string) {
	if val, ok := c.Get(key); ok && val != nil {
		ss, _ = val.([]string)
	}
	return
}

// GetStringMap returns the value associated with the key as a map of interfaces.
func (c *storage) GetStringMap(key string) (sm map[string]any) {
	if val, ok := c.Get(key); ok && val != nil {
		sm, _ = val.(map[string]interface{})
	}
	return
}

// GetStringMapString returns the value associated with the key as a map of strings.
func (c *storage) GetStringMapString(key string) (sms map[string]string) {
	if val, ok := c.Get(key); ok && val != nil {
		sms, _ = val.(map[string]string)
	}
	return
}

// GetStringMapStringSlice returns the value associated with the key as a map to a slice of strings.
func (c *storage) GetStringMapStringSlice(key string) (smss map[string][]string) {
	if val, ok := c.Get(key); ok && val != nil {
		smss, _ = val.(map[string][]string)
	}
	return
}

type client struct {
	isClose bool               //是否已关闭
	ctx     Context            //协程控制
	cancel  context.CancelFunc //协程控制

	options *Options

	lock sync.Mutex //锁

}

type onOpen func(*Context)

type onClose func(*Context)

type onMessage func(*Context, []byte)

type onError func(*Context, error)

var closeErrorCode = []int{
	websocket.CloseNormalClosure,
	websocket.CloseGoingAway,
	websocket.CloseProtocolError,
	websocket.CloseUnsupportedData,
	websocket.CloseNoStatusReceived,
	websocket.CloseAbnormalClosure,
	websocket.CloseInvalidFramePayloadData,
	websocket.ClosePolicyViolation,
	websocket.CloseMessageTooBig,
	websocket.CloseMandatoryExtension,
	websocket.CloseInternalServerErr,
	websocket.CloseServiceRestart,
	websocket.CloseTryAgainLater,
	websocket.CloseTLSHandshake,
}

// 实际执行开始的函数
func (c *client) init() error {
	c.ctx.Ctx, c.cancel = context.WithCancel(c.ctx.Ctx)
	c.ctx.sendChan = make(chan []byte, 100)
	c.ctx.secondSendChan = make(chan []byte, 100)
	c.isClose = false
	go c.writeWs()
	if c.options.onMessage != nil {
		go c.readWs()
	}
	c.openFn()
	return nil
}

// 实际执行结束的函数
func (c *client) close() {
	c.lock.Lock()
	defer c.lock.Unlock()
	if c.isClose || c.ctx.conn == nil {
		c.isClose = true
		return
	}
	c.isClose = true
	c.cancel()
	c.closeFn()

}

// 发送协程
func (c *client) writeWs() {
	defer func() {
		if err := recover(); err != nil {
			c.errorFn(fmt.Errorf("发送协程出现异常: %v", err))
		}
	}()
	for {
		select {
		case data := <-c.ctx.sendChan:
			c.write(data)
		case secondData := <-c.ctx.secondSendChan:
		priority:
			for {
				select {
				case data := <-c.ctx.sendChan:
					if len(data) == 0 {
						break priority
					}
					c.write(data)
				default:
					break priority
				}
			}
			c.write(secondData)
		case <-c.ctx.Ctx.Done():
		}
		if c.isClose {
			break
		}
	}
	_ = c.safeCloseChan(c.ctx.sendChan)
	for data := range c.ctx.sendChan {
		_ = c.ctx.conn.WriteMessage(websocket.TextMessage, data)
	}
	_ = c.safeCloseChan(c.ctx.secondSendChan)
	for data := range c.ctx.secondSendChan {
		_ = c.ctx.conn.WriteMessage(websocket.TextMessage, data)
	}
	_ = c.ctx.conn.Close()
}

// 接收消息
func (c *client) readWs() {
	defer func() {
		if err := recover(); err != nil {
			c.errorFn(fmt.Errorf("读取协程出现异常: %v", err))
		}
		if !c.isClose {
			c.close()
		}
	}()
	for {
		_, message, err := c.ctx.conn.ReadMessage()
		if err != nil {
			c.errorFn(err)
			if _, ok := err.(*net.OpError); ok || websocket.IsCloseError(err, closeErrorCode...) || c.isClose {
				return
			}
			continue
		}
		c.options.onMessage(&c.ctx, message)
	}
}

func (c *client) write(data []byte) {
	err := c.ctx.conn.WriteMessage(websocket.TextMessage, data)
	if err != nil {
		c.errorFn(err)
		if websocket.IsCloseError(err, closeErrorCode...) {
			c.close()
			return
		}
	}
}

func (c *client) safeCloseChan(ch chan []byte) (justClosed bool) {
	defer func() {
		if recover() != nil {
			justClosed = false
		}
	}()
	// assume ch != nil here.
	close(ch)   // panic if ch is closed
	return true // <=> justClosed = true; return
}

// 执行结束闭包函数
func (c *client) errorFn(err error) {
	if c.options.onError != nil {
		c.options.onError(&c.ctx, err)
	}
}

// 执行连接成功闭包函数
func (c *client) openFn() {
	if c.options.onOpen != nil {
		c.options.onOpen(&c.ctx)
	}
}

// //执行关闭闭包函数
func (c *client) closeFn() {
	if c.options.onClose != nil {
		c.options.onClose(&c.ctx)
	}
}

/*************************************************WS链接内部逻辑end***************************************************/
