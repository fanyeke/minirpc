package codec

import "io"

// 一个RPC调用: err = client.Call("Arith.Multiply", args, &reply)
// 客户端发送的请求参数: 服务名 `Arith` 方法名 `Multiply` 参数 `args`
// 服务端返回的相应: `error` `reply`
// 将请求的参数和返回值抽象为body, 剩余信息放在 `Header` 中

// Header 抽象出来的 `Header`
type Header struct {
	ServiceMethod string // 服务名和方法, 与结构体的方法映射
	Seq           uint64 // 请求的序号, 可认为是某个请求的ID, 用来区分不同的请求
	Error         string // 错误信息
}

// Codec 实现编解码的接口
type Codec interface {
	io.Closer                         // 关闭方法
	ReadHeader(*Header) error         // 读方法, 读 `Header` 信息, `Header` 信息有格式, 可以传入 `*Header`
	ReadBody(interface{}) error       // 读方法, 读 `Body` 信息, `Body` 接受任何类型的格式
	Write(*Header, interface{}) error // 写方法, 写回返回值
}

type NewCodecFunc func(io.ReadWriteCloser) Codec

type Type string

const (
	GobType  Type = "applocation/gob"
	JsonType Type = "applocation/json"
)

// NewCodecFuncMap 根据编解码类型(Type)映射不同的函数
var NewCodecFuncMap map[Type]NewCodecFunc

func init() {
	NewCodecFuncMap = make(map[Type]NewCodecFunc) // 初始化映射Map
	NewCodecFuncMap[GobType] = NewGobCodec        // 注册编解码Gob的函数
}
