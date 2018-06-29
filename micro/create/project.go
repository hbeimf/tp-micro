package create

import (
	"bytes"
	"encoding/json"
	"fmt"
	"go/format"
	"html/template"
	"os"
	"path"
	"regexp"
	"strconv"
	"strings"
	"unsafe"

	"github.com/henrylee2cn/goutil"
	tp "github.com/henrylee2cn/teleport"
	"github.com/xiaoenai/tp-micro/micro/info"
)

type (
	// Project project Information
	Project struct {
		*tplInfo
		codeFiles    map[string]string
		Name         string
		ImprotPrefix string
	}
	Model struct {
		*structType
		ModelStyle       string
		PrimaryFields    []*field
		UniqueFields     []*field
		Fields           []*field
		IsDefaultPrimary bool
		Doc              string
		Name             string
		SnakeName        string
		LowerFirstName   string
		LowerFirstLetter string
		NameSql          string
		QuerySql         [2]string
		UpdateSql        string
		UpsertSqlSuffix  string
	}
)

// NewProject new project.
func NewProject(src []byte) *Project {
	p := new(Project)
	p.tplInfo = newTplInfo(src).Parse()
	p.Name = info.ProjName()
	p.ImprotPrefix = info.ProjPath()
	p.codeFiles = make(map[string]string)
	for k, v := range tplFiles {
		p.codeFiles[k] = v
	}
	for k := range p.codeFiles {
		p.fillFile(k)
	}
	return p
}

func (p *Project) fillFile(k string) {
	v, ok := p.codeFiles[k]
	if !ok {
		return
	}
	v = strings.Replace(v, "${import_prefix}", p.ImprotPrefix, -1)
	switch k {
	case "main.go", "config.go", "logic/model/init.go":
		p.codeFiles[k] = v
	case "logic/tmp_code.gen.go":
		p.codeFiles[k] = "// Code generated by 'micro gen' command.\n" +
			"// The temporary code used to ensure successful compilation!\n" +
			"// When the project is completed, it should be removed!\n\n" + v
	default:
		p.codeFiles[k] = "// Code generated by 'micro gen' command.\n// DO NOT EDIT!\n\n" + v
	}
}

func mustMkdirAll(dir string) {
	err := os.MkdirAll(dir, os.ModePerm)
	if err != nil {
		tp.Fatalf("[micro] %v", err)
	}
}

func hasGenSuffix(name string) bool {
	switch name {
	case "README.md", ".gitignore", "main.go", "config.go", "args/const.go",
		"args/var.go", "args/type.go", "api/handler.go", "api/router.go",
		"sdk/rpc.go", "sdk/rpc_test.go", "logic/model/init.go":
		return false
	default:
		return true
	}
}

func (p *Project) Generator(force, newdoc bool) {
	p.gen()
	// make all directorys
	mustMkdirAll("args")
	mustMkdirAll("api")
	mustMkdirAll("logic/model")
	mustMkdirAll("sdk")
	// write files
	for k, v := range p.codeFiles {
		if !force && !hasGenSuffix(k) {
			continue
		}
		realName := info.ProjPath() + "/" + k
		f, err := os.OpenFile(k, os.O_WRONLY|os.O_TRUNC|os.O_CREATE, os.ModePerm)
		if err != nil {
			tp.Fatalf("[micro] create %s error: %v", realName, err)
		}
		b := formatSource(goutil.StringToBytes(v))
		f.Write(b)
		f.Close()
		fmt.Printf("generate %s\n", realName)
	}

	// gen and write README.md
	if newdoc {
		p.genAndWriteReadmeFile()
	}
}

// generate all codes
func (p *Project) gen() {
	p.genMainFile()
	p.genConstFile()
	p.genTypeFile()
	p.genRouterFile()
	p.genHandlerFile()
	p.genLogicFile()
	p.genSdkFile()
	p.genModelFile()
}

func (p *Project) genAndWriteReadmeFile() {
	f, err := os.OpenFile("./README.md", os.O_WRONLY|os.O_TRUNC|os.O_CREATE, os.ModePerm)
	if err != nil {
		tp.Fatalf("[micro] create README.md error: %v", err)
	}
	f.WriteString(p.genReadme())
	f.Close()
	fmt.Printf("generate %s\n", info.ProjPath()+"/README.md")
}

func commentToHtml(txt string) string {
	return strings.TrimLeft(strings.Replace(txt, "// ", "<br>", -1), "<br>")
}

func (p *Project) genReadme() string {
	var text string
	text += commentToHtml(p.tplInfo.doc)
	text += "\n"
	text += "## API Desc\n\n"
	for _, h := range p.tplInfo.HandlerList() {
		text += fmt.Sprintf("### %s\n\n%s\n\n", h.fullName, p.handlerDesc(h))
	}
	r := strings.Replace(__readme__, "${PROJ_NAME}", info.ProjName(), -1)
	r = strings.Replace(r, "${readme}", text, 1)
	return r
}

func (p *Project) handlerDesc(h *handler) string {
	rootGroup := goutil.SnakeString(p.Name)
	uri := path.Join("/", rootGroup, h.uri)
	var text string
	text += commentToHtml(h.doc) + "\n"
	text += fmt.Sprintf("- URI:\n\t```\n\t%s\n\t```\n", uri)

	var fn = func(name string, txt string) {
		fields, _ := p.tplInfo.lookupTypeFields(name)
		if len(fields) == 0 {
			text += fmt.Sprintf("- %s:\n", txt)
		} else {
			text += fmt.Sprintf("- %s:\n", txt)
			jsonStr := p.fieldsJson(fields)
			var dst bytes.Buffer
			json.Indent(&dst, []byte(jsonStr), "\t", "\t")
			jsonStr = p.replaceCommentJson(dst.String())
			text += fmt.Sprintf("\t```json\n\t%s\n\t```\n", jsonStr)
		}
	}

	fn(h.arg, "REQUEST")
	fn(h.result, "RESULT")

	return text
}

var ptrStringRegexp = regexp.MustCompile(`(\$\d+)":.*[,\n]{1}`)

func (p *Project) replaceCommentJson(s string) string {
	a := ptrStringRegexp.FindAllStringSubmatch(s, -1)
	for _, ss := range a {
		sub := strings.Replace(ss[0], ss[1], "", 1)
		ptr, _ := strconv.Atoi(ss[1][1:])
		f := (*field)(unsafe.Pointer(uintptr(ptr)))
		doc := f.doc
		if len(doc) == 0 {
			doc = f.comment
		}
		doc = strings.TrimSpace(strings.Replace(doc, "\n//", "", -1))
		if sub[len(sub)-1] == ',' {
			s = strings.Replace(s, ss[0], sub+"\t"+doc, 1)
		} else {
			s = strings.Replace(s, ss[0], sub[:len(sub)-1]+"\t"+doc+"\n", 1)
		}
	}
	return s
}

func (p *Project) fieldsJson(fs []*field) string {
	if len(fs) == 0 {
		return ""
	}
	var text string
	text += "{"
	for _, f := range fs {
		fieldName := f.ModelName
		if len(fieldName) == 0 {
			fieldName = goutil.SnakeString(f.Name)
		}
		t := strings.Replace(f.Typ, "*", "", -1)
		var isSlice bool
		if strings.HasPrefix(t, "[]") {
			if t == "[]byte" {
				t = "string"
			} else {
				t = strings.TrimPrefix(t, "[]")
				isSlice = true
			}
		}
		v, ok := baseTypeToJsonValue(t)
		if ok {
			if isSlice {
				text += fmt.Sprintf(`"%s$%d":[%s],`, fieldName, uintptr(unsafe.Pointer(f)), v)
			} else {
				text += fmt.Sprintf(`"%s$%d":%s,`, fieldName, uintptr(unsafe.Pointer(f)), v)
			}
			continue
		}
		if ffs, ok := p.tplInfo.lookupTypeFields(t); ok {
			if isSlice {
				text += fmt.Sprintf(`"%s":[%s],`, fieldName, p.fieldsJson(ffs))
			} else {
				text += fmt.Sprintf(`"%s":%s,`, fieldName, p.fieldsJson(ffs))
			}
			continue
		}
	}
	text = strings.TrimRight(text, ",") + "}"
	return text
}

func baseTypeToJsonValue(t string) (string, bool) {
	if t == "bool" {
		return "false", true
	} else if t == "string" || t == "[]byte" || t == "time.Time" {
		return `""`, true
	} else if strings.HasPrefix(t, "int") || t == "rune" {
		return "-0", true
	} else if strings.HasPrefix(t, "uint") || t == "byte" {
		return "0", true
	} else if strings.HasPrefix(t, "float") {
		return "-0.000000", true
	}
	return "", false
}

func (p *Project) genMainFile() {
	p.replace("main.go", "${service_api_prefix}", goutil.SnakeString(p.Name))
	p.replace("config.go", "${service_api_prefix}", goutil.SnakeString(p.Name))
}

func (p *Project) genConstFile() {
	var text string
	for _, s := range p.tplInfo.models.mysql {
		name := s.name + "Sql"
		text += fmt.Sprintf(
			"// %s the statement to create '%s' mysql table\n"+
				"const %s string = ``\n",
			name, goutil.SnakeString(s.name),
			name,
		)
	}
	p.replaceWithLine("args/const.gen.go", "${const_list}", text)
}

func (p *Project) genTypeFile() {
	p.replaceWithLine("args/type.gen.go", "${import_list}", p.tplInfo.TypeImportString())
	p.replaceWithLine("args/type.gen.go", "${type_define_list}", p.tplInfo.TypesString())
}

func (p *Project) genRouterFile() {
	p.replaceWithLine(
		"api/router.gen.go",
		"${register_router_list}",
		p.tplInfo.RouterString("_group"),
	)
}

func (p *Project) genHandlerFile() {
	if len(p.tplInfo.PushHandlerList()) > 0 {
		s := p.tplInfo.PushHandlerString(func(h *handler) string {
			var ctx = "ctx"
			if len(h.group.name) > 0 {
				ctx = firstLowerLetter(h.group.name) + ".PushCtx"
			}
			return fmt.Sprintf("return logic.%s(%s, arg)", h.fullName, ctx)
		})
		p.replaceWithLine("api/push_handler.gen.go", "${handler_api_define}", s)
	} else {
		delete(p.codeFiles, "api/push_handler.gen.go")
		os.Remove("api/push_handler.gen.go")
	}
	if len(p.tplInfo.PullHandlerList()) > 0 {
		s := p.tplInfo.PullHandlerString(func(h *handler) string {
			var ctx = "ctx"
			if len(h.group.name) > 0 {
				ctx = firstLowerLetter(h.group.name) + ".PullCtx"
			}
			return fmt.Sprintf("return logic.%s(%s, arg)", h.fullName, ctx)
		})
		p.replaceWithLine("api/pull_handler.gen.go", "${handler_api_define}", s)
	} else {
		delete(p.codeFiles, "api/pull_handler.gen.go")
		os.Remove("api/pull_handler.gen.go")
	}
}

func (p *Project) genLogicFile() {
	var s string
	for _, h := range p.tplInfo.HandlerList() {
		name := h.fullName
		switch h.group.typ {
		case pullType:
			s += fmt.Sprintf(
				"%sfunc %s(ctx tp.PullCtx,arg *args.%s)(*args.%s,*tp.Rerror){\nreturn new(args.%s),nil\n}\n\n",
				h.doc, name, h.arg, h.result, h.result,
			)
		case pushType:
			s += fmt.Sprintf(
				"%sfunc %s(ctx tp.PushCtx,arg *args.%s)*tp.Rerror{\nreturn nil\n}\n\n",
				h.doc, name, h.arg,
			)
		}
	}
	p.replaceWithLine("logic/tmp_code.gen.go", "${logic_api_define}", s)
}

func (p *Project) genSdkFile() {
	var s1, s2 string
	for _, h := range p.tplInfo.HandlerList() {
		name := h.fullName
		uri := path.Join("/", goutil.SnakeString(p.Name), h.uri)
		switch h.group.typ {
		case pullType:
			s1 += fmt.Sprintf(
				"%sfunc %s(arg *args.%s, setting ...socket.PacketSetting)(*args.%s,*tp.Rerror){\n"+
					"result := new(args.%s)\n"+
					"rerr := client.Pull(\"%s\", arg, result, setting...).Rerror()\n"+
					"return result, rerr\n}\n",
				h.doc, name, h.arg, h.result,
				h.result,
				uri,
			)
			s2 += fmt.Sprintf(
				"{\n"+
					"result, rerr :=%s(new(args.%s))\n"+
					"if rerr != nil {\ntp.Errorf(\"%s: rerr: %%v\", rerr)\n} else {\ntp.Infof(\"%s: result: %%#v\", result)\n}\n"+
					"}\n",
				name, h.arg, name, name,
			)
		case pushType:
			s1 += fmt.Sprintf(
				"%sfunc %s(arg *args.%s, setting ...socket.PacketSetting)*tp.Rerror{\n"+
					"return client.Push(\"%s\", arg, setting...)\n}\n",
				h.doc, name, h.arg,
				uri,
			)
			s2 += fmt.Sprintf(
				"{\n"+
					"rerr :=%s(new(args.%s))\n"+
					"if rerr != nil {\ntp.Errorf(\"%s: rerr: %%v\", rerr)\n}\n"+
					"}\n",
				name, h.arg, name,
			)
		}
	}
	p.replaceWithLine("sdk/rpc.gen.go", "${rpc_call_define}", s1)
	p.replaceWithLine("sdk/rpc.gen_test.go", "${rpc_call_test_define}", s2)
}

func (p *Project) genModelFile() {
	for _, m := range p.tplInfo.models.mysql {
		fileName := "logic/model/mysql_" + goutil.SnakeString(m.name) + ".gen.go"
		p.codeFiles[fileName] = newModelString(m)
		p.fillFile(fileName)
	}
	for _, m := range p.tplInfo.models.mongo {
		fileName := "logic/model/mongo_" + goutil.SnakeString(m.name) + ".gen.go"
		p.codeFiles[fileName] = newModelString(m)
		p.fillFile(fileName)
	}
}

func newModelString(s *structType) string {
	model := &Model{
		structType:       s,
		PrimaryFields:    s.primaryFields,
		UniqueFields:     s.uniqueFields,
		IsDefaultPrimary: s.isDefaultPrimary,
		Fields:           s.fields,
		Doc:              s.doc,
		Name:             s.name,
		ModelStyle:       s.modelStyle,
		SnakeName:        goutil.SnakeString(s.name),
	}
	model.LowerFirstLetter = strings.ToLower(model.Name[:1])
	model.LowerFirstName = model.LowerFirstLetter + model.Name[1:]
	switch s.modelStyle {
	case "mysql":
		return model.mysqlString()
	case "mongo":
		return model.mongoString()
	}
	return ""
}

func (mod *Model) mongoString() string {
	mod.NameSql = fmt.Sprintf("`%s`", mod.SnakeName)
	mod.QuerySql = [2]string{}
	mod.UpdateSql = ""
	mod.UpsertSqlSuffix = ""

	var (
		fields               []string
		querySql1, querySql2 string
	)
	for _, field := range mod.fields {
		fields = append(fields, field.ModelName)
	}
	var primaryFields []string
	var primaryFieldMap = make(map[string]bool)
	for _, field := range mod.PrimaryFields {
		primaryFields = append(primaryFields, field.ModelName)
		primaryFieldMap[field.ModelName] = true
	}
	for _, field := range fields {
		if field == "deleted_ts" || primaryFieldMap[field] {
			continue
		}
		querySql1 += fmt.Sprintf("`%s`,", field)
		querySql2 += fmt.Sprintf(":%s,", field)
		if field == "created_at" {
			continue
		}
		mod.UpdateSql += fmt.Sprintf("`%s`=:%s,", field, field)
		mod.UpsertSqlSuffix += fmt.Sprintf("`%s`=VALUES(`%s`),", field, field)
	}
	mod.QuerySql = [2]string{querySql1[:len(querySql1)-1], querySql2[:len(querySql2)-1]}
	mod.UpdateSql = mod.UpdateSql[:len(mod.UpdateSql)-1]
	mod.UpsertSqlSuffix = mod.UpsertSqlSuffix[:len(mod.UpsertSqlSuffix)-1] + ";"

	m, err := template.New("").Parse(mongoModelTpl)
	if err != nil {
		tp.Fatalf("[micro] model string: %v", err)
	}
	buf := bytes.NewBuffer(nil)
	err = m.Execute(buf, mod)
	if err != nil {
		tp.Fatalf("[micro] model string: %v", err)
	}
	s := strings.Replace(buf.String(), "&lt;", "<", -1)
	return strings.Replace(s, "&gt;", ">", -1)
}

func (mod *Model) mysqlString() string {
	mod.NameSql = fmt.Sprintf("`%s`", mod.SnakeName)
	mod.QuerySql = [2]string{}
	mod.UpdateSql = ""
	mod.UpsertSqlSuffix = ""

	var (
		fields               []string
		querySql1, querySql2 string
	)
	for _, field := range mod.fields {
		fields = append(fields, field.ModelName)
	}
	var primaryFields []string
	var primaryFieldMap = make(map[string]bool)
	for _, field := range mod.PrimaryFields {
		primaryFields = append(primaryFields, field.ModelName)
		primaryFieldMap[field.ModelName] = true
	}
	for _, field := range fields {
		if field == "deleted_ts" || primaryFieldMap[field] {
			continue
		}
		querySql1 += fmt.Sprintf("`%s`,", field)
		querySql2 += fmt.Sprintf(":%s,", field)
		if field == "created_at" {
			continue
		}
		mod.UpdateSql += fmt.Sprintf("`%s`=:%s,", field, field)
		mod.UpsertSqlSuffix += fmt.Sprintf("`%s`=VALUES(`%s`),", field, field)
	}
	mod.QuerySql = [2]string{querySql1[:len(querySql1)-1], querySql2[:len(querySql2)-1]}
	mod.UpdateSql = mod.UpdateSql[:len(mod.UpdateSql)-1]
	mod.UpsertSqlSuffix = mod.UpsertSqlSuffix[:len(mod.UpsertSqlSuffix)-1] + ";"

	m, err := template.New("").Parse(mysqlModelTpl)
	if err != nil {
		tp.Fatalf("[micro] model string: %v", err)
	}
	buf := bytes.NewBuffer(nil)
	err = m.Execute(buf, mod)
	if err != nil {
		tp.Fatalf("[micro] model string: %v", err)
	}
	s := strings.Replace(buf.String(), "&lt;", "<", -1)
	return strings.Replace(s, "&gt;", ">", -1)
}

func (p *Project) replace(key, placeholder, value string) string {
	a := strings.Replace(p.codeFiles[key], placeholder, value, -1)
	p.codeFiles[key] = a
	return a
}

func (p *Project) replaceWithLine(key, placeholder, value string) string {
	return p.replace(key, placeholder, "\n"+value)
}

func formatSource(src []byte) []byte {
	b, err := format.Source(src)
	if err != nil {
		tp.Fatalf("[micro] format error: %v\ncode:\n%s", err, src)
	}
	return b
}
