package main

import (
	"fmt"
	"github.com/fatih/structtag"
	flag "github.com/spf13/pflag"
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"golang.org/x/tools/go/packages"
	"log"
	"os"
	"path"
	"regexp"
	"strconv"
	"strings"
)

const appName = "configuration"

func main() {
	err := run(os.Args)
	if err != nil {
		log.Fatalln(err)
	}
}

type Strategy string

const (
	Unknown Strategy = "Unknown"
	Native           = "Native"
	Set              = "Set"
	Enumer           = "Enumer"
	Marshal          = "Marshal"
	Func             = "Func"
	Scan             = "Scan"
)

type Field struct {
	Name           string
	Tag            *structtag.Tag
	AstField       *ast.Field `json:"-"`
	FlagName       string
	EnvName        string
	Strategy       Strategy
	NativeFn       string
	TypePackage    string
	TypeName       string
	TypeElement    string
	TypeBasename   string
	TypeImportPath string
	Description    string
	Hidden         bool
}

type state struct {
	fields []*Field
	//functions    []*ast.FuncDecl
	packageName  string
	packagePath  string
	configStruct string
	envPrefix    string
	outputFile   string
	err          error
	imports      []*ast.ImportSpec
	files        map[string]*ast.File
	pflagPath    string
	pflagName    string
	commandLine  string
}

func run(args []string) error {
	vis := &state{
		commandLine: strings.Join(args, " "),
		pflagPath:    "github.com/spf13/pflag",
	}

	fset := token.NewFileSet()

	cfg := &packages.Config{
		Mode: packages.NeedName | packages.NeedTypes | packages.NeedSyntax,
		Fset: fset,
		ParseFile: func(fset *token.FileSet, filename string, src []byte) (*ast.File, error) {
			return parser.ParseFile(fset, filename, src, parser.ParseComments)
		},
	}

	flagset := flag.NewFlagSet(appName, flag.ContinueOnError)
	flagset.StringVar(&vis.envPrefix, "prefix", "", "prefix for environment variables")
	flagset.StringVar(&vis.outputFile, "out", "", "generate output to file (or - for stdout)")
	flagset.BoolVar(&cfg.Tests, "parseTests", false, "look inside test files for configuration")
	if err := flagset.Parse(args[1:]); err != nil {
		return err
	}

	if flagset.NArg() != 1 {
		return fmt.Errorf("usage: %s [--prefix=<EnvPrefix>] StructName", appName)
	}

	vis.configStruct = flagset.Arg(0)


	pkgs, err := packages.Load(cfg, "")
	if err != nil {
		return fmt.Errorf("failed to load source: %w", err)
	}

	for _, pkg := range pkgs {
		fmt.Printf("found package %s %s\n", pkg.Name ,pkg.PkgPath)
		fmt.Println(pkg)
		fmt.Println(pkg.Syntax)
		vis.packageName = pkg.Name
		vis.packagePath = pkg.PkgPath

		for _, file := range pkg.Syntax {
			fmt.Printf("Walking %s\n", file.Name.String())
			ast.Walk(vis, file)
		}
		if vis.err != nil {
			return vis.err
		}
		if vis.fields != nil {
			return generate(vis, pkg)
		}
	}
	return nil
}

func (vis *state) Visit(n ast.Node) ast.Visitor {
	//f, ok := n.(*ast.FuncDecl)
	//if ok {
	//	vis.functions = append(vis.functions, f)
	//}

	i, ok := n.(*ast.ImportSpec)
	if ok {
		vis.imports = append(vis.imports, i)
	}

	t, ok := n.(*ast.TypeSpec)
	if !ok {
		return vis
	}

	if t.Type == nil {
		return vis
	}

	structName := t.Name.Name
	if structName != vis.configStruct {
		return vis
	}
	vis.fields = nil
	s, ok := t.Type.(*ast.StructType)
	if !ok {
		return vis
	}

	for _, field := range s.Fields.List {
		name := field.Names[0].Name
		f := Field{
			Name:     name,
			AstField: field,
			FlagName: toFlag(name),
			EnvName:  toEnvName(vis.envPrefix, name),
			Strategy: Unknown,
			Hidden:   false,
		}

		// Read our struct tags
		if field.Tag != nil && field.Tag.Kind == token.STRING {
			tagstr, err := strconv.Unquote(field.Tag.Value)
			if err != nil {
				log.Fatalf("bad quoting in struct tag '%s': %v", field.Tag.Value, err)
			}
			tags, err := structtag.Parse(tagstr)
			if err != nil {
				log.Fatalf("bad struct tag '%s': %v", field.Tag.Value, err)
			}
			tag, err := tags.Get("config")
			if err == nil {
				// tag exists
				if tag.Name == "-" {
					continue
				}
				if tag.Name != "" {
					f.FlagName = toFlag(tag.Name)
					f.EnvName = toEnvName(vis.envPrefix, tag.Name)
				}

				f.Tag = tag
				if tag.HasOption("set") {
					f.Strategy = Set
				}
				if tag.HasOption("enum") {
					f.Strategy = Enumer
				}
				if tag.HasOption("func") {
					f.Strategy = Func
				}
				if tag.HasOption("marshal") {
					f.Strategy = Marshal
				}
				if tag.HasOption("hidden") {
					f.Hidden = true
				}
			}
		}

		switch v := field.Type.(type) {
		case *ast.Ident, *ast.SelectorExpr:
			f.TypeName = typeName(v)
		case *ast.ArrayType:
			if v.Len != nil {
				vis.err = fmt.Errorf("cannot handle non-slice array in %s.%s", vis.configStruct, name)
				return nil
			}

			elType := typeName(v.Elt)
			if elType != "" {
				f.TypeElement = elType
				f.TypeName = "[]" + elType
			}
		case *ast.MapType:
			vis.err = fmt.Errorf("cannot handle map type in %s.%s", vis.configStruct, name)
			return nil
		default:
			vis.err = fmt.Errorf("cannot handle %T in %s.%s\n", field.Type, vis.configStruct, name)
			return nil
		}

		if f.TypeName == "" {
			vis.err = fmt.Errorf("cannot handle type for %s.%s", vis.configStruct, name)
			return nil
		}

		if field.Doc != nil {
			f.Description = strings.TrimSpace(field.Doc.Text())
		}
		if field.Comment != nil {
			f.Description = strings.TrimSpace(field.Comment.Text())
		}

		vis.fields = append(vis.fields, &f)
	}

	// do stuff

	return vis

}

var matchFirstCap = regexp.MustCompile("(.)([A-Z][a-z]+)")
var matchAllCap = regexp.MustCompile("([a-z0-9])([A-Z])")

func toSnakeCase(str string) string {
	snake := matchFirstCap.ReplaceAllString(str, "${1}_${2}")
	snake = matchAllCap.ReplaceAllString(snake, "${1}_${2}")
	return strings.ToLower(snake)
}

func toEnvName(prefix, str string) string {
	envName := strings.ToUpper(toSnakeCase(str))
	if prefix == "" {
		return envName
	}
	return prefix + "_" + envName
}

func toFlag(str string) string {
	return strings.ReplaceAll(toSnakeCase(str), "_", "-")
}

func typeName(t ast.Expr) string {
	switch v := t.(type) {
	case *ast.Ident:
		return v.Name
	case *ast.SelectorExpr:
		p, ok := v.X.(*ast.Ident)
		if ok {
			return p.Name + "." + v.Sel.Name
		}
	}
	return ""
}

type Import struct {
	Name string
	Path string
}

type Result struct {
	Package      string
	Command      string
	ConfigStruct string
	Pflag        string
	Imports      []Import
	Fields       []*Field
}

func generate(vis *state, pkg *packages.Package) error {
	// Handle the types pflag understands
	additionalImports := map[string]string{
		"os": "",
		"strings": "",
	}
	for _, f := range vis.fields {
		if f.TypeElement != "" {
			// It's an array
			additionalImports["strings"] = ""
		}
		if f.Strategy != Unknown {
			continue
		}
		fn := ""
		switch f.TypeName {
		case "[]bool":
			fn = "BoolSliceVar"
		case "bool":
			fn = "BoolVar"
		case "[]byte":
			fn = "BytesHexVar"
		case "time.Duration":
			fn = "DurationVar"
		case "[]time.Duration":
			fn = "DurationSliceVar"
		case "float32":
			fn = "Float32Var"
		case "[]float32":
			fn = "Float32SliceVar"
		case "float64":
			fn = "Float64Var"
		case "[]float64":
			fn = "Float64SliceVar"
		case "net.IP":
			fn = "IPVar"
		case "[]net.IP":
			fn = "IPSliceVar"
		case "net.IPMask":
			fn = "IPMaskVar"
		case "net.IPNet":
			fn = "IPNetVar"
		case "int":
			fn = "IntVar"
		case "[]int":
			fn = "IntSliceVar"
		case "int8":
			fn = "Int8Var"
		case "int16":
			fn = "Int16Var"
		case "int32":
			fn = "Int32Var"
		case "[]int32":
			fn = "Int32SliceVar"
		case "int64":
			fn = "Int64Var"
		case "[]int64":
			fn = "Int64SliceVar"
		case "string":
			fn = "StringVar"
		case "[]string":
			fn = "StringSliceVar"
		case "uint":
			fn = "UintVar"
		case "[]uint":
			fn = "UintSliceVar"
		case "uint16":
			fn = "Uint16Var"
		case "uint32":
			fn = "Uint32Var"
		case "uint64":
			fn = "Uint64Var"
		}
		if fn != "" {
			f.Strategy = Native
			f.NativeFn = fn
			continue
		}
	}

	imports := map[string]string{}
	for _, im := range vis.imports {
		pkgpath, _ := strconv.Unquote(im.Path.Value)
		if im.Name == nil {
			imports[path.Base(pkgpath)] = pkgpath
		} else {
			imports[im.Name.String()] = pkgpath
		}
	}

	//fmt.Printf("%s: %s %s\n", vis.packageName, vis.packagePath, vis.configStruct)

	//for _, fn := range vis.functions {
	//	fmt.Printf("Function: %s\n", fn.Name.Name)
	//}

	importsNeeded := map[string]struct{}{}

	for _, f := range vis.fields {
		if f.Strategy != Unknown {
			continue
		}

		tp := strings.TrimPrefix(f.TypeName, "[]")
		parts := strings.Split(tp, ".")
		if len(parts) != 2 {
			f.TypeBasename = f.TypeName
			f.TypeImportPath = pkg.PkgPath
			continue
		}
		packageName := parts[0]
		f.TypeBasename = parts[1]

		pkgPath, ok := imports[packageName]
		if !ok {
			return fmt.Errorf("cannot find import '%s' for %s.%s", packageName, vis.configStruct, f.Name)
		}

		importsNeeded[pkgPath] = struct{}{}
		f.TypeImportPath = pkgPath
	}

	for _, f := range vis.fields {
		if f.Strategy != Unknown {
			continue
		}
		//fmt.Printf("==== Looking for String for %s %s\n", f.Name, f.TypeBasename)
		// Look for an enumer-style assigner
		fname := f.TypeBasename + "String"
		ob := pkg.Types.Scope().Lookup(fname)
		if ob != nil {
			fn, ok := ob.(*types.Func)
			if ok {
				sig, ok := fn.Type().(*types.Signature)
				if ok {
					//fmt.Printf("got sig [%v] [%v]\n", sig.Recv(), f.TypeName)
					if sig.Recv() == nil && functionMatches(sig, []string{"string"}, []string{f.TypeImportPath + "." + f.TypeName, "error"}) {
						f.Strategy = Enumer
					}
				}
			}
		}
	}

	importsToFetch := make([]string, 0, len(importsNeeded))
	for v := range importsNeeded {
		importsToFetch = append(importsToFetch, v)
	}

	// NeedImports seems to be required to avoid errors, even though we don't use it
	cfg := &packages.Config{
		Mode: packages.NeedName | packages.NeedTypes | packages.NeedImports,
	}
	pkgs, err := packages.Load(cfg, importsToFetch...)
	if err != nil {
		return fmt.Errorf("failed to load imports: %w", err)
	}
	pkgs = append(pkgs, pkg)

	for _, p := range pkgs {
		//fmt.Printf("Package name: %s path: %s\n", p.Name, p.PkgPath)
		scope := p.Types.Scope()
		//fmt.Println(scope.Names())
		for _, f := range vis.fields {
			if f.Strategy != Unknown {
				continue
			}
			//fmt.Printf("Name: %s f.P: %s p.P: %s\n", f.Name, f.TypeImportPath, p.PkgPath)
			if f.TypeImportPath != p.PkgPath {
				continue
			}
			//fmt.Printf("Looking up %s %s\n", f.Name, f.TypeBasename)
			// Find the type
			ob := scope.Lookup(f.TypeBasename)
			if ob != nil {
				//fmt.Printf("Found ob for %s\n", f.Name)
				typePtr := types.NewPointer(ob.Type())

				// Find methods with a pointer receiver
				methodSet := types.NewMethodSet(typePtr)

				// Do we have `func () Set(string) error`?
				if methodExists(methodSet, p.Types, "Set", []string{"string"}, []string{"error"}) {
					//fmt.Printf("Found Set\n")
					f.Strategy = Set
					continue
				}

				// Do we have `func () UmarshalText([]byte) error`?
				if methodExists(methodSet, p.Types, "UnmarshalText", []string{"[]byte"}, []string{"error"}) {
					//fmt.Printf("Found UnmarshalText\n")
					f.Strategy = Marshal
					continue
				}

				// Do we have `func () Scan(interface{}) error`?
				if methodExists(methodSet, p.Types, "Scan", []string{"interface{}"}, []string{"error"}) {
					f.Strategy = Scan
					continue
				}
			}
		}
	}

	//for _, f := range vis.fields {
	//	fmt.Printf("Name: %s Type: %s Strategy: %s Flag: %s Env: %s '%s'\n", f.Name, f.TypeName, f.Strategy, f.FlagName, f.EnvName, f.Description)
	//}

	//j, err := json.MarshalIndent(vis.fields, "", "  ")
	//if err != nil {
	//	return err
	//}
	//
	//fmt.Println(string(j))

	data := &Result{
		Package:      vis.packageName,
		Command:      vis.commandLine,
		ConfigStruct: vis.configStruct,
		Imports:      []Import{},
		Fields:       vis.fields,
	}

	importsNeeded[vis.pflagPath] = struct{}{}
	pflagIncluded := false
	for k, v := range imports {
		if v == vis.pflagPath {
			data.Pflag = k
			pflagIncluded = true
		}
		if _, ok := importsNeeded[v]; ok {
			data.Imports = append(data.Imports, Import{
				Name: k,
				Path: v,
			})
		}
	}
	if !pflagIncluded {
		data.Imports = append(data.Imports, Import{
			Name: "",
			Path: vis.pflagPath,
		})
	}

	for k, v := range additionalImports {
		if _, ok := importsNeeded[k]; !ok {
			data.Imports = append(data.Imports, Import{
				Name: v,
				Path: k,
			})
		}
	}

	if data.Pflag == "" {
		data.Pflag = path.Base(vis.pflagPath)
	}

	res, err := RenderGoTemplate("loader.tpl", data)

	if vis.outputFile == "" {
		vis.outputFile = strings.ToLower(vis.configStruct)+".gen.go"
	}

	if err != nil {
		_ = writeFile(vis.outputFile, res, 0644)
		return err
	}

	return writeFile(vis.outputFile, res, 0644)
}

func writeFile(filename string, content []byte, perm os.FileMode) error {
	if filename == "-" {
		_, err := os.Stdout.Write(content)
		return err
	}
	return os.WriteFile(filename, content, perm)
}

func methodExists(methodSet *types.MethodSet, pkg *types.Package, methodName string, params, returns []string) bool {
	// Find method by name
	sel := methodSet.Lookup(pkg, methodName)
	if sel == nil {
		return false
	}
	funcDetails, ok := sel.Obj().(*types.Func)
	if !ok {
		return false
	}
	sig, ok := funcDetails.Type().(*types.Signature)
	if !ok {
		return false
	}

	return functionMatches(sig, params, returns)
}

func functionMatches(sig *types.Signature, params, returns []string) bool {
	// Check parameters
	funcParams := sig.Params()
	//fmt.Printf("Params: [%v] [%v]\n", funcParams, params)
	if funcParams.Len() != len(params) {
		return false
	}
	for i, v := range params {
		//fmt.Printf("Comparing [%s] and [%s]\n", v, funcParams.At(i).Type().String())
		if v != funcParams.At(i).Type().String() {
			return false
		}
	}

	// Check return values
	funcReturns := sig.Results()
	if funcReturns.Len() != len(returns) {
		return false
	}
	for i, v := range returns {
		//fmt.Printf("Comparing [%s] and [%s]\n", v, funcReturns.At(i).Type().String())
		if v != funcReturns.At(i).Type().String() {
			return false
		}
	}
	return true
}
