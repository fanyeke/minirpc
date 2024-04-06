package minirpc

import (
	"go/ast"
	"log"
	"reflect"
	"sync/atomic"
)

type methodType struct {
	method    reflect.Method
	ArgType   reflect.Type
	ReplyType reflect.Type
	numCalls  uint64
}

func (m *methodType) NumCalls() uint64 {
	return atomic.LoadUint64(&m.numCalls)
}

// newArgv 方法用于创建一个新的参数值
func (m *methodType) newArgv() reflect.Value {
	var argv reflect.Value
	// 如果ArgType是指针就获取它的值并返回
	if m.ArgType.Kind() == reflect.Ptr {
		argv = reflect.New(m.ArgType.Elem())
	} else {
		argv = reflect.New(m.ArgType).Elem()
	}
	return argv
}

// newReplyv 方法用于创建一个新的返回值
func (m *methodType) newReplyv() reflect.Value {
	replyv := reflect.New(m.ReplyType.Elem())
	switch m.ReplyType.Elem().Kind() {
	case reflect.Map:
		replyv.Elem().Set(reflect.MakeMap(m.ReplyType.Elem()))
	case reflect.Slice:
		replyv.Elem().Set(reflect.MakeSlice(m.ReplyType.Elem(), 0, 0))
	}
	return replyv
}

type service struct {
	name   string                 // 映射的结构体的名称
	typ    reflect.Type           // 结构体的类型
	rcvr   reflect.Value          // 结构体的实例本身
	method map[string]*methodType // 存储映射的结构体符合条件的所有方法
}

// newService 初始化
func newService(rcvr interface{}) *service {
	s := new(service)
	s.rcvr = reflect.ValueOf(rcvr)                  // 将rcvr设置为它的内容
	s.name = reflect.Indirect(s.rcvr).Type().Name() // 设置为它的名称
	s.typ = reflect.TypeOf(rcvr)                    // 设置为它的类型

	// 如果名称不是大写开头, 说明是不可导出类型, 不可能调用到, 直接报错
	if !ast.IsExported(s.name) {
		log.Fatalf("rpc server: %s is not a valid service name", s.name)
	}
	s.registerMethods()
	return s
}

// registerMethods 注册方法
func (s *service) registerMethods() {
	// 初始化映射map
	s.method = make(map[string]*methodType)
	// 遍历结构体中的所有方法
	for i := 0; i < s.typ.NumMethod(); i++ {
		method := s.typ.Method(i) // 获得方法
		mType := method.Type      // 获得方法的类型
		// NumIn 获得入参数量 NumOut 获得出参数量
		// * 规定返回入参必须是三个, 出参必须是一个
		if mType.NumIn() != 3 || mType.NumOut() != 1 {
			continue
		}
		// Out 返回第i个参数的类型
		// * 规定这唯一的出参必须是error类型
		if mType.Out(0) != reflect.TypeOf((*error)(nil)).Elem() {
			continue
		}
		// 获得入参的第二三个的参数的类型
		argType, replyType := mType.In(1), mType.In(2)
		// 鉴别两个参数类型是否合法
		if !isExportedOrBuiltinType(argType) || !isExportedOrBuiltinType(replyType) {
			continue
		}
		// 以上都合法, 将这个方法进行注册
		s.method[method.Name] = &methodType{
			method:    method,
			ArgType:   argType,
			ReplyType: replyType,
		}
		//fmt.Println(s.method[method.Name])
		log.Printf("rpc server: register %s.%s\n", s.name, method.Name)
	}

}

func isExportedOrBuiltinType(t reflect.Type) bool {
	// 如果开头不大写 或者 没有包名, 返回false
	return ast.IsExported(t.Name()) || t.PkgPath() == ""
}

// call 调用指定方法
func (s *service) call(m *methodType, argv, replyv reflect.Value) error {
	// ? 调用次数+1
	atomic.AddUint64(&m.numCalls, 1)
	f := m.method.Func                                            // 取出这个方法
	returnValues := f.Call([]reflect.Value{s.rcvr, argv, replyv}) // 执行这个方法
	// 因为能够注册的函数第一个参数就是error类型, 因此这里的returnValues[0]就是error类型
	if errInter := returnValues[0].Interface(); errInter != nil {
		return errInter.(error)
	}
	return nil
}
