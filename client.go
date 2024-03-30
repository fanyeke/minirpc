package minirpc

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"sync"

	"github.com/fanyeke/minirpc/codec"
)

type Call struct {
	Seq           uint64 // 请求编号
	ServiceMethod string
	Args          interface{}
	Reply         interface{}
	Error         error
	Done          chan *Call
}

// done Done 的类型是 chan *Call, 当调用结束时, 会调用 call.done() 通知调用方
func (call *Call) done() {
	call.Done <- call
}

// Client 客户端结构体
type Client struct {
	cc       codec.Codec // 消息的编解码器
	opt      *Option
	sending  sync.Mutex   // 保证请求有序发送
	header   codec.Header // 请求消息头
	mu       sync.Mutex
	seq      uint64           // 请求编号
	pending  map[uint64]*Call // 储存未完成的请求
	closing  bool             // 用户主动关闭
	shutdown bool             // 发生错误关闭, 都代表 `Client` 处于不可用状态
}

var _ io.Closer = (*Client)(nil)

var ErrShutdown = errors.New("connection is shut down")

// Close 实现 `io.Closer` 接口
func (client *Client) Close() error {
	client.mu.Lock()
	defer client.mu.Unlock()
	// 客户端已经被主动关闭, 返回自定义错误
	if client.closing {
		return ErrShutdown
	}
	// 服务端还被主动关闭, 则将其置为关闭状态
	client.closing = true
	// 关闭链接
	return client.cc.Close()
}

// IsAvailable 返回当前客户端是否可用
func (client *Client) IsAvailable() bool {
	client.mu.Lock()
	defer client.mu.Unlock()
	// 两个状态都没有被置为关闭方可返回 `true`
	return !client.shutdown && !client.closing
}

// registerCall 注册请求
func (client *Client) registerCall(call *Call) (uint64, error) {
	client.mu.Lock()
	defer client.mu.Unlock()
	if client.closing || client.shutdown {
		return 0, ErrShutdown
	}
	// 客户端seq传递给请求的seq
	call.Seq = client.seq
	// 注册到map中
	client.pending[call.Seq] = call
	client.seq++
	return call.Seq, nil
}

// remoceCall 移除请求
func (client *Client) removeCall(seq uint64) *Call {
	client.mu.Lock()
	defer client.mu.Unlock()
	call := client.pending[seq]
	delete(client.pending, seq)
	return call
}

// terminateCalls 终止请求
func (client *Client) terminateCalls(err error) {
	client.sending.Lock()
	defer client.sending.Unlock()
	client.mu.Lock()
	defer client.mu.Unlock()
	client.shutdown = true
	for _, call := range client.pending {
		call.Error = err
		call.done()
	}
}

// erceive 接受服务的处理结果
func (client *Client) receive() {
	var err error
	for err == nil {
		var h codec.Header
		// 解码服务器消息中的 `Header`
		if err = client.cc.ReadHeader(&h); err != nil {
			break
		}
		// 完成请求, 不论如何删除并拿到当初的 `Call`
		call := client.removeCall(h.Seq)

		/*
		   1. call 不存在，可能是请求没有发送完整，或者因为其他原因被取消，但是服务端仍旧处理了。
		   2. call 存在，但服务端处理出错，即 h.Error 不为空。
		   3. call 存在，服务端处理正常，那么需要从 body 中读取 Reply 的值。
		*/
		switch {
		case call == nil:
			err = client.cc.ReadBody(nil)
		case h.Error != "":
			call.Error = fmt.Errorf(h.Error)
			err = client.cc.ReadBody(nil)
			call.done()
		default:
			err = client.cc.ReadBody(call.Reply)
			if err != nil {
				call.Error = errors.New("reading body" + err.Error())
			}
			call.done()
		}
	}
	// 因为是不断轮询的, 因此走到这里一定是发生错误, 终止所有请求
	client.terminateCalls(err)
}

// NewClient 创建一个客户端
func NewClient(conn net.Conn, opt *Option) (*Client, error) {
	// 客户端获取编解码的函数
	f := codec.NewCodecFuncMap[opt.CodecType]

	// 如果编解码函数没有定义
	if f == nil {
		err := fmt.Errorf("invalid codec error: %s", opt.CodecType)
		log.Println("rpc client: codec error:", err)
		return nil, err
	}
	// 读取编解码的设置
	if err := json.NewEncoder(conn).Encode(opt); err != nil {
		log.Println("rpc client: options err:", err)
		_ = conn.Close()
		return nil, err
	}
	// 创建客户端编解码器
	return newClientCodec(f(conn), opt), nil
}

// newClientCodec 创建客户端
func newClientCodec(cc codec.Codec, opt *Option) *Client {
	client := &Client{
		seq:     1,
		cc:      cc,
		opt:     opt,
		pending: make(map[uint64]*Call),
	}
	// 开启轮询接受消息
	go client.receive()
	return client
}

// parseOption 解析配置
func parseOption(opts ...*Option) (*Option, error) {
	if len(opts) == 0 || opts[0] == nil {
		return DefaultOption, nil
	}
	if len(opts) != 1 {
		return nil, errors.New("number of options is more than 1")
	}
	opt := opts[0]
	opt.MagicNumber = DefaultOption.MagicNumber
	if opt.CodecType == "" {
		opt.CodecType = DefaultOption.CodecType
	}
	return opt, nil
}

// Dial 方便传入服务器地址
func Dial(network, address string, opts ...*Option) (client *Client, err error) {
	// 解析配置
	opt, err := parseOption(opts...)
	if err != nil {
		return nil, err
	}
	// 与服务器进行链接
	conn, err := net.Dial(network, address)
	if err != nil {
		return nil, err
	}
	defer func() {
		// 链接建立未成功, 及时关闭链接
		if client == nil {
			_ = conn.Close()
		}
	}()
	return NewClient(conn, opt)
}

// send 客户端发送请求, 传入参数是一个call
func (client *Client) send(call *Call) {
	client.sending.Lock()
	defer client.sending.Unlock()
	// 首先把这个call注册到映射map中
	seq, err := client.registerCall(call)
	if err != nil {
		call.Error = err
		call.done()
		return
	}
	// 定义请求消息的头部
	client.header.ServiceMethod = call.ServiceMethod
	client.header.Seq = seq
	client.header.Error = ""
	// 发送请求消息
	if err := client.cc.Write(&client.header, call.Args); err != nil {
		call := client.removeCall(seq)
		if call != nil {
			call.Error = err
			call.done()
		}
	}
}

// Go 异步调用函数
func (client *Client) Go(serviceMethod string, args, reply interface{}, done chan *Call) *Call {
	// 如果 done 没有初始化 或者 初始化容量为0
	if done == nil {
		done = make(chan *Call, 10)
	} else if cap(done) == 0 {
		log.Panic("rpc client: done is unbuffered")
	}
	call := &Call{
		ServiceMethod: serviceMethod,
		Args:          args,
		Reply:         reply,
		Done:          done,
	}
	client.send(call)
	return call
}

// Call 调用指定函数, 并等待其返回, 返回它的错误
func (client *Client) Call(serverMethod string, args, reply interface{}) error {
	call := <-client.Go(serverMethod, args, reply, make(chan *Call, 1)).Done
	return call.Error
}
