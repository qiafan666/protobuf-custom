// Copyright 2010 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// protoc-gen-go is a plugin for the Google protocol buffer compiler to generate
// Go code. Install it by building this program and making it accessible within
// your PATH with the name:
//
//	protoc-gen-go
//
// The 'go' suffix becomes part of the argument for the protocol compiler,
// such that it can be invoked as:
//
//	protoc --go_out=paths=source_relative:. path/to/file.proto
//
// This generates Go bindings for the protocol buffer defined by file.proto.
// With that input, the output will be written to:
//
//	path/to/file.pb.go
//
// See the README and documentation for protocol buffers to learn more:
//
//	https://developers.google.com/protocol-buffers/
package main

import (
	"flag"
	"fmt"
	"github.com/golang/protobuf/internal/gengogrpc"
	gengo "github.com/golang/protobuf/protobuf/cmd/protoc-gen-go/internal_gengo"
	"github.com/golang/protobuf/protobuf/compiler/protogen"
	"strings"
)

func main() {
	var (
		flags        flag.FlagSet
		plugins      = flags.String("plugins", "", "list of plugins to enable (supported values: grpc)")
		importPrefix = flags.String("import_prefix", "", "prefix to prepend to import paths")
	)
	importRewriteFunc := func(importPath protogen.GoImportPath) protogen.GoImportPath {
		switch importPath {
		case "context", "fmt", "math":
			return importPath
		}
		if *importPrefix != "" {
			return protogen.GoImportPath(*importPrefix) + importPath
		}
		return importPath
	}
	protogen.Options{
		ParamFunc:         flags.Set,
		ImportRewriteFunc: importRewriteFunc,
	}.Run(func(gen *protogen.Plugin) error {
		grpc := false
		kite := false
		ctxKite := false
		for _, plugin := range strings.Split(*plugins, ",") {
			switch plugin {
			case "grpc":
				grpc = true
			case "kite":
				kite = true
			case "ctx":
				ctxKite = true

			default:
				if plugin != "" {
					return fmt.Errorf("protoc-gen-go: unknown plugin %q", plugin)
				}
			}
		}
		for _, f := range gen.Files {
			if !f.Generate {
				continue
			}
			g := gengo.GenerateFile(gen, f)
			if kite {
				genConsulRpc(f, g)
			}
			if ctxKite {
				genConsulCtxRpc(f, g)
			}
			if grpc {
				gengogrpc.GenerateFileContent(gen, f, g)
			}
		}
		gen.SupportedFeatures = gengo.SupportedFeatures
		return nil
	})
}

const (
	SRPC     = "srpc"
	SRPCPATH = "meta/pkg/srpc"
	PB       = "pb"
	PBPATH   = "meta/pkg/srpc/pb"
)

func genConsulRpc(f *protogen.File, g *protogen.GeneratedFile) {
	//导包
	if len(f.Services) > 0 && len(f.Services[0].Methods) > 0 {
		g.QualifiedGoIdent(protogen.GoIdent{
			GoName:       "errors",
			GoImportPath: "errors",
		})

		g.QualifiedGoIdent(protogen.GoIdent{
			GoName:       SRPC,
			GoImportPath: SRPCPATH,
		})

		g.QualifiedGoIdent(protogen.GoIdent{
			GoName:       PB,
			GoImportPath: PBPATH,
		})
	}

	// 生成服务结构体和实例
	for _, service := range f.Services {
		if len(service.Methods) <= 0 {
			continue
		}
		serviceName := service.GoName
		structName := strings.ToLower(serviceName[:1]) + serviceName[1:]

		// 生成服务结构体和实例
		g.P(fmt.Sprintf(`// %s rpc客户端实例`, serviceName))
		g.P(fmt.Sprintf("var %s = &%s{}", serviceName, structName))
		g.P(fmt.Sprintf("type %s struct {}", structName))

		//去除proto文件的.proto后缀和/之前的所有字符
		var protoName string
		if strings.HasSuffix(f.Proto.GetName(), ".proto") {
			protoName = f.Proto.GetName()[:len(f.Proto.GetName())-6]
		}
		if strings.LastIndex(protoName, "/") > 0 {
			protoName = protoName[strings.Index(protoName, "/")+1:]
		}

		// 生成客户端方法
		for _, method := range service.Methods {
			methodName := method.GoName
			reqType := method.Input.GoIdent.GoName
			resType := method.Output.GoIdent.GoName

			g.P(fmt.Sprintf(`
// %s 通过destination调用consul rpc服务
func (c *%s) %s(destination %s.Destination, request *%s, opts ...%s.Option) (response *%s, err error) {
	reqPBData, err := %s.Marshal(request)
	if err != nil {
		return nil, errors.New("request marshal err")
	}
	resPBData, err := %s.Invoke(destination, "%s", "%s", "%s", reqPBData, opts...)
	if err != nil {
		return nil, err
	}
	response = new(%s)
	err = %s.Unmarshal(resPBData, response)
	return
}`, methodName, structName, methodName, SRPC, reqType, SRPC, resType, PB, SRPC, protoName, serviceName, methodName, resType, PB))
		}

		// 生成服务接口头
		g.P(fmt.Sprintf(`
// %sServer is the server API for %s service.
type %s interface {`, serviceName, serviceName, serviceName+"Server"))

		for _, method := range service.Methods {
			methodName := method.GoName
			reqType := method.Input.GoIdent.GoName
			resType := method.Output.GoIdent.GoName
			g.P(fmt.Sprintf(`%s(*%s) (*%s, error)`, methodName, reqType, resType))
		}
		//生成服务接口屁股
		g.P(`}`)

		// 生成服务实现
		g.P(fmt.Sprintf(`
type %sService struct {
	handle %sServer
}
 
// Reg%sServer 注册%s服务
func Reg%sServer(handle %sServer) {
	%s.ServiceDispatchObject.AddService("%s", "%s", &%sService{handle: handle})
}`, serviceName, serviceName, serviceName, serviceName, serviceName, serviceName, PB, protoName, serviceName, serviceName))

		// 生成Do方法
		g.P(fmt.Sprintf(`
func (s *%sService) Do(function string, reqPBData []byte) (resPBData []byte, err error) {
	switch function {`, serviceName))

		// 在 Do 方法中添加每个方法的 case 语句
		for _, method := range service.Methods {
			methodName := method.GoName
			g.P(fmt.Sprintf(`	case "%s":`, methodName))
			g.P(fmt.Sprintf(`		return s.%s(function,reqPBData)`, methodName))
		}

		// 添加 default 语句
		g.P(`	default:`)
		g.P(`		err = errors.New("function is not found")`)
		g.P(`	}`)
		g.P(`	return`)
		g.P(`}`)

		// 为每个方法生成对应的实现
		for _, method := range service.Methods {
			methodName := method.GoName
			reqType := method.Input.GoIdent.GoName
			resType := method.Output.GoIdent.GoName

			g.P(fmt.Sprintf(`
func (s *%sService) %s(function string, reqPBData []byte) (resPBData []byte, err error) {
	req := new(%s)
	%s.Unmarshal(reqPBData, req)
	res := new(%s)
	res, err = s.handle.%s(req)
	if err == nil {
		resPBData, err = %s.Marshal(res)
	}
	return
}`, serviceName, methodName, reqType, PB, resType, methodName, PB))
		}
	}
}
func genConsulCtxRpc(f *protogen.File, g *protogen.GeneratedFile) {
	//导包
	if len(f.Services) > 0 && len(f.Services[0].Methods) > 0 {
		g.QualifiedGoIdent(protogen.GoIdent{
			GoName:       "errors",
			GoImportPath: "errors",
		})

		g.QualifiedGoIdent(protogen.GoIdent{
			GoName:       SRPC,
			GoImportPath: SRPCPATH,
		})

		g.QualifiedGoIdent(protogen.GoIdent{
			GoName:       PB,
			GoImportPath: PBPATH,
		})
		g.QualifiedGoIdent(protogen.GoIdent{
			GoName:       "context",
			GoImportPath: "context",
		})
	}

	// 生成服务结构体和实例
	for _, service := range f.Services {
		if len(service.Methods) <= 0 {
			continue
		}
		serviceName := service.GoName
		structName := strings.ToLower(serviceName[:1]) + serviceName[1:]

		// 生成服务结构体和实例
		g.P(fmt.Sprintf(`// %s rpc客户端实例`, serviceName))
		g.P(fmt.Sprintf("var %s = &%s{}", serviceName, structName))
		g.P(fmt.Sprintf("type %s struct {}", structName))

		//去除proto文件的.proto后缀和/之前的所有字符
		var protoName string
		if strings.HasSuffix(f.Proto.GetName(), ".proto") {
			protoName = f.Proto.GetName()[:len(f.Proto.GetName())-6]
		}
		if strings.LastIndex(protoName, "/") > 0 {
			protoName = protoName[strings.Index(protoName, "/")+1:]
		}

		// 生成客户端方法
		for _, method := range service.Methods {
			methodName := method.GoName
			reqType := method.Input.GoIdent.GoName
			resType := method.Output.GoIdent.GoName

			g.P(fmt.Sprintf(`
// %s 通过destination调用consul rpc服务
func (c *%s) %s(ctx context.Context, destination %s.Destination, request *%s, opts ...%s.Option) (response *%s, err error) {
	reqPBData, err := %s.Marshal(request)
	if err != nil {
		return nil, errors.New("request marshal err")
	}
	resPBData, err := %s.Invoke(ctx, destination, "%s", "%s", "%s", reqPBData, opts...)
	if err != nil {
		return nil, err
	}
	response = new(%s)
	err = %s.Unmarshal(resPBData, response)
	return
}`, methodName, structName, methodName, SRPC, reqType, SRPC, resType, PB, SRPC, protoName, serviceName, methodName, resType, PB))
		}

		// 生成服务接口头
		g.P(fmt.Sprintf(`
// %sServer is the server API for %s service.
type %s interface {`, serviceName, serviceName, serviceName+"Server"))

		for _, method := range service.Methods {
			methodName := method.GoName
			reqType := method.Input.GoIdent.GoName
			resType := method.Output.GoIdent.GoName
			g.P(fmt.Sprintf(`%s(context.Context, *%s) (*%s, error)`, methodName, reqType, resType))
		}
		//生成服务接口屁股
		g.P(`}`)

		// 生成服务实现
		g.P(fmt.Sprintf(`
type %sService struct {
	handle %sServer
}
 
// Reg%sServer 注册%s服务
func Reg%sServer(handle %sServer) {
	%s.ServiceDispatchObject.AddService("%s", "%s", &%sService{handle: handle})
}`, serviceName, serviceName, serviceName, serviceName, serviceName, serviceName, PB, protoName, serviceName, serviceName))

		// 生成Do方法
		g.P(fmt.Sprintf(`
func (s *%sService) Do(ctx context.Context, function string, reqPBData []byte) (resPBData []byte, err error) {
	switch function {`, serviceName))

		// 在 Do 方法中添加每个方法的 case 语句
		for _, method := range service.Methods {
			methodName := method.GoName
			g.P(fmt.Sprintf(`	case "%s":`, methodName))
			g.P(fmt.Sprintf(`		return s.%s(ctx,function,reqPBData)`, methodName))
		}

		// 添加 default 语句
		g.P(`	default:`)
		g.P(`		err = errors.New("function is not found")`)
		g.P(`	}`)
		g.P(`	return`)
		g.P(`}`)

		// 为每个方法生成对应的实现
		for _, method := range service.Methods {
			methodName := method.GoName
			reqType := method.Input.GoIdent.GoName
			resType := method.Output.GoIdent.GoName

			g.P(fmt.Sprintf(`
func (s *%sService) %s(ctx context.Context, function string, reqPBData []byte) (resPBData []byte, err error) {
	req := new(%s)
	%s.Unmarshal(reqPBData, req)
	res := new(%s)
	res, err = s.handle.%s(ctx, req)
	if err == nil {
		resPBData, err = %s.Marshal(res)
	}
	return
}`, serviceName, methodName, reqType, PB, resType, methodName, PB))
		}
	}
}

//如何对json和bson的tag名称进行修改
/*
1、将google.golang.org/protobuf的包下载到本地，删除其中的go.mod文件
2、["google.golang.org/protobuf] 替换为 ["github.com/golang/protobuf/protobuf]
3、将导入的包路径改为之前的"google.golang.org/protobuf  位置在protobuf/cmd/protoc-gen-go/internal_gengo 影响生成的pb.go文件导入路径
4、以下代码粘贴到protoc-gen-go/internal_gengo/main.go 411行，修改json和bson的tag名称

	jsonUnderscoreName := func(name string) string {
		// 使用 strings.Builder 代替自定义的 NewBuffer
		var buffer strings.Builder
		for i, r := range name {
			if unicode.IsUpper(r) {
				if i != 0 {
					buffer.WriteRune('_') // 使用 WriteRune 追加下划线字符
				}
				buffer.WriteRune(unicode.ToLower(r)) // 将大写字母转为小写
			} else {
				buffer.WriteRune(r) // 保持非大写字母原样追加
			}
		}

		return buffer.String()
	}

	bsonUnderscoreName := func(name string) string {
		if strings.ToLower(name) == "id" {
			return "_id"
		}

		var buffer strings.Builder
		for i, r := range name {
			if unicode.IsUpper(r) {
				if i != 0 {
					buffer.WriteRune('_') // 使用 WriteRune 追加下划线字符
				}
				buffer.WriteRune(unicode.ToLower(r)) // 将大写字母转为小写
			} else {
				buffer.WriteRune(r) // 保持非大写字母原样追加
			}
		}

		return buffer.String()
	}

tags := structTags{
{"protobuf", fieldProtobufTagValue(field)},
{"json", jsonUnderscoreName(string(field.Desc.Name())) + ",omitempty"},
{"bson", bsonUnderscoreName(string(field.Desc.Name()))},
}*/
