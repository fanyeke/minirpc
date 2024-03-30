package minirpc

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"reflect"
	"sync"

	"github.com/fanyeke/minirpc/codec"
)

// | Option{MagicNumber: xxx, CodecType: xxx} | Header{ServiceMethod ...} | Body interface{} |
// | <------      固定 JSON 编码      ------>  | <-------   编码方式由 CodeType 决定   ------->|
// 传输实例:
// | Option | Header1 | Body1 | Header2 | Body2 | ...

const MagicNumber = 0x3bef5c

// Option 固定 JSON 编码, 用于定义之后的传输编码方式
type Option struct {
	MagicNumber int
	CodecType   codec.Type
}

// DefaultOption 默认编码方式
var DefaultOption = &Option{
	MagicNumber: MagicNumber,
	CodecType:   codec.GobType,
}

type Server struct {
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

	server.serverCodec(f(conn))
}

// invalidRequest 是发生错误时响应 argv 的占位符
var invalidRequest = struct{}{}

// serverCodec
func (server *Server) serverCodec(cc codec.Codec) {
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
		go server.handleRequest(cc, req, sending, wg)
	}
	wg.Wait()
	_ = cc.Close()
}

type request struct {
	h            *codec.Header
	argv, replyv reflect.Value
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
	// TODO: 不知道请求的argv

	req.argv = reflect.New(reflect.TypeOf(""))
	if err = cc.ReadBody(req.argv.Interface()); err != nil {
		log.Println("rpc server: read argv err: ", err)
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
func (server *Server) handleRequest(cc codec.Codec, req *request, sending *sync.Mutex, wg *sync.WaitGroup) {
	// TODO: 应调用已注册的 rpc 方法以获得正确的replyv
	defer wg.Done()
	log.Println(req.h, req.argv.Elem())
	req.replyv = reflect.ValueOf(fmt.Sprintf("minirpc resp %d", req.h.Seq))
	server.sendResponse(cc, req.h, req.replyv.Interface(), sending)
}
