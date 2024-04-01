package minirpc

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"reflect"
	"strings"
	"sync"
	"time"

	"github.com/fanyeke/minirpc/codec"
)

// | Option{MagicNumber: xxx, CodecType: xxx} | Header{ServiceMethod ...} | Body interface{} |
// | <------      固定 JSON 编码      ------>  | <-------   编码方式由 CodeType 决定   ------->|
// 传输实例:
// | Option | Header1 | Body1 | Header2 | Body2 | ...

const MagicNumber = 0x3bef5c

// Option 固定 JSON 编码, 用于定义之后的传输编码方式
type Option struct {
	MagicNumber    int
	CodecType      codec.Type
	ConnectTimeout time.Duration // 建立链接超时
	HandleTimeout  time.Duration // 请求处理超时
}

// DefaultOption 默认编码方式
var DefaultOption = &Option{
	MagicNumber:    MagicNumber,
	CodecType:      codec.GobType,
	ConnectTimeout: 10 * time.Second, // 连接超时设置为10s
}

type Server struct {
	serviceMap sync.Map
}

func (server *Server) Register(rcvr interface{}) error {
	s := newService(rcvr)
	if _, dup := server.serviceMap.LoadOrStore(s.name, s); dup {
		return errors.New("rpc: service already defined: " + s.name)
	}
	return nil
}

func Register(rcvr interface{}) error {
	return DefaultServer.Register(rcvr)
}

func (server *Server) findService(serviceMethod string) (svc *service, mtype *methodType, err error) {
	dot := strings.LastIndex(serviceMethod, ".") // 获得函数的名称下标
	if dot < 0 {                                 // 如果传入serviceMethod不合法写回错误
		err = errors.New("rpc server: service/method request ill-formed: " + serviceMethod)
		return
	}
	// 记录请求函数的 包名 和 方法名
	serviceName, methodName := serviceMethod[:dot], serviceMethod[dot+1:]
	svci, ok := server.serviceMap.Load(serviceName) // 获得 `serviceMap` 中存储的值
	if !ok {
		err = errors.New("rpc server: can't find service " + serviceName)
		return
	}
	// 断言为 service 类型
	svc = svci.(*service)
	// 从 service 中获取到方法
	mtype = svc.method[methodName]
	if mtype == nil {
		err = errors.New("rpc server: can't find method " + methodName)
	}
	return
}

func NewServer() *Server {
	return &Server{}
}

// DefaultServer 默认的 `Server`
var DefaultServer = NewServer()

// Accept 接受一个 `lis` 监听端口
func (server *Server) Accept(lis net.Listener) {
	for {
		conn, err := lis.Accept()
		if err != nil {
			log.Println("rpc server: accept error:", err)
			return
		}
		go server.ServerConn(conn)
	}
}

// Accept 使用默认的 `DefaultServer` 去监听
func Accept(lis net.Listener) {
	DefaultServer.Accept(lis)
}

// ServerConn 传入 `socket` 链接实例
func (server *Server) ServerConn(conn io.ReadWriteCloser) {
	defer func() {
		_ = conn.Close()
	}()

	var opt Option
	// json.NewDecoder 函数创建一个新的 JSON 解码器，
	// 并使用 Decode 方法将从 conn 读取的 JSON 数据解码到 opt 变量中

	// json.NewDecoder().Decode() 是从一个 io.Reader 中逐步读取数据，
	// 然后解析这些数据。这意味着它不需要一次性在内存中存储整个 JSON 数据，
	// 而是可以逐步处理数据。这在处理大型流式 JSON 数据时，可以更有效地管理内存。
	if err := json.NewDecoder(conn).Decode(&opt); err != nil {
		log.Println("rpc server: options error: ", opt.MagicNumber)
		return
	}

	// 如果读取到的编码方式与默认编码方式不同
	if opt.MagicNumber != MagicNumber {
		log.Printf("rpc server: invalid magic number %x", opt.MagicNumber)
		return
	}
	// 获取编解码注册的相应函数
	f := codec.NewCodecFuncMap[opt.CodecType]
	// 如果没有注册对应的函数
	if f == nil {
		log.Printf("rpc server: invalid codec type %s", opt.CodecType)
		return
	}

	server.serverCodec(f(conn), &opt)
}

// invalidRequest 是发生错误时响应 argv 的占位符
var invalidRequest = struct{}{}

// serverCodec
func (server *Server) serverCodec(cc codec.Codec, opt *Option) {
	sending := new(sync.Mutex) // 确保完整回复
	wg := new(sync.WaitGroup)
	for {
		// 从 `socket` 链接实例中获取请求
		req, err := server.readRequest(cc)
		if err != nil {
			// 请求为空, 忽略并跳过此次轮询
			if req == nil {
				break
			}
			req.h.Error = err.Error()
			// 将错误写回响应, 不进行处理
			server.sendResponse(cc, req.h, invalidRequest, sending)
			continue
		}
		wg.Add(1)
		// 处理请求
		go server.handleRequest(cc, req, sending, wg, opt.HandleTimeout)
	}
	wg.Wait()
	_ = cc.Close()
}

type request struct {
	h            *codec.Header
	argv, replyv reflect.Value
	mtype        *methodType
	svc          *service
}

// readRequestHeader 读取请求 `Header`
func (server *Server) readRequestHeader(cc codec.Codec) (*codec.Header, error) {
	var h codec.Header
	if err := cc.ReadHeader(&h); err != nil {
		if err != io.EOF && err != io.ErrUnexpectedEOF {
			log.Println("rpc server: read header error: ", err)
		}
		return nil, err
	}
	return &h, nil
}

// readRequest 读取请求
func (server *Server) readRequest(cc codec.Codec) (*request, error) {
	h, err := server.readRequestHeader(cc)
	if err != nil {
		return nil, err
	}
	req := &request{h: h}
	req.svc, req.mtype, err = server.findService(h.ServiceMethod)
	if err != nil {
		return req, err
	}
	// 使用之前要先初始化, 创建出入参实例
	req.argv = req.mtype.newArgv()
	req.replyv = req.mtype.newReplyv()
	argvi := req.argv.Interface()
	if req.argv.Type().Kind() != reflect.Ptr {
		argvi = req.argv.Addr().Interface()
	}
	if err = cc.ReadBody(argvi); err != nil {
		log.Println("rpc server: read argv err: ", err)
		return req, err
	}
	return req, nil
}

// sendRespense 写回响应
func (server *Server) sendResponse(cc codec.Codec, h *codec.Header, body interface{}, sending *sync.Mutex) {
	sending.Lock()
	defer sending.Unlock()

	if err := cc.Write(h, body); err != nil {
		log.Println("rpc server: write response error:", err)
	}
}

// handleRequest 处理请求
func (server *Server) handleRequest(cc codec.Codec, req *request, sending *sync.Mutex, wg *sync.WaitGroup, timeout time.Duration) {

	defer wg.Done()
	// struct{}{} 类型的 channel 很明显就是为了传输信号
	called := make(chan struct{})
	sent := make(chan struct{})
	go func() {
		// 调用包含在请求字段的方法
		err := req.svc.call(req.mtype, req.argv, req.replyv)
		// 方法调用完毕, 通知 called
		called <- struct{}{}
		if err != nil {
			// 出错误了, 把错误携带上
			req.h.Error = err.Error()
			// 响应请求
			server.sendResponse(cc, req.h, invalidRequest, sending)
			// 响应已经发送
			sent <- struct{}{}
			return
		}
		server.sendResponse(cc, req.h, req.replyv.Interface(), sending)
		sent <- struct{}{}
	}()
	// 没有超时控制则一直阻塞等待, 直到请求处理完毕并且发送了响应
	if timeout == 0 {
		<-called
		<-sent
		return
	}
	// 有超时控制
	select {
	case <-time.After(timeout):
		req.h.Error = fmt.Sprintf("rpc server: request timeout: expect within %s", timeout)
		server.sendResponse(cc, req.h, invalidRequest, sending)
	case <-called:
		<-sent
	}
}
