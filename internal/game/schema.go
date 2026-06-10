package game

// Field describes one configurable parameter of a task type. The dashboard
// uses these to render graphical forms (text/number/checkbox inputs) instead
// of asking the operator to hand-write params JSON. Keys map 1:1 onto the
// params JSON the adapters read in BuildCommand.
type Field struct {
	Key         string `json:"key"`
	Label       string `json:"label"`
	Type        string `json:"type"` // text | number | bool | json
	Required    bool   `json:"required,omitempty"`
	Default     any    `json:"default,omitempty"`
	Placeholder string `json:"placeholder,omitempty"`
	Help        string `json:"help,omitempty"`
}

// TaskTypeInfo describes one task type of an adapter for UI purposes.
type TaskTypeInfo struct {
	Type   string  `json:"type"`
	Label  string  `json:"label"`
	Desc   string  `json:"desc,omitempty"`
	Fields []Field `json:"fields"`
}

// rawType is the universal escape hatch every adapter supports.
var rawType = TaskTypeInfo{
	Type: "raw", Label: "自定义命令", Desc: "直接给出完整命令行参数,绕过默认拼接;工具版本参数不一致时用这个",
	Fields: []Field{
		{Key: "raw_args", Label: "参数列表 (JSON 数组)", Type: "json", Required: true, Placeholder: `["-t","1","-e"]`},
		{Key: "exe", Label: "可执行文件覆盖", Type: "text", Placeholder: "留空使用游戏的 tool_path"},
	},
}

// schemas defines the graphical form for each adapter's task types. The type
// keys must stay in sync with each adapter's TaskTypes() (guarded by a test).
var schemas = map[string][]TaskTypeInfo{
	"genshin": {
		{Type: "onedragon", Label: "一条龙", Desc: "运行 BetterGI 一条龙(签到/委托/采集等按其配置执行)",
			Fields: []Field{{Key: "group", Label: "配置组", Type: "text", Placeholder: "留空运行默认一条龙"}}},
		{Type: "config_group", Label: "调度器配置组", Desc: "运行 BetterGI 调度器里的某个配置组(地图追踪/采集/锄地路线)",
			Fields: []Field{{Key: "group", Label: "配置组名称", Type: "text", Required: true, Placeholder: "如 采集"}}},
		{Type: "script", Label: "JS 脚本", Desc: "运行单个 BetterGI JS 脚本(bettergi-scripts-list 订阅的脚本)",
			Fields: []Field{{Key: "script", Label: "脚本名或路径", Type: "text", Required: true, Placeholder: "如 AutoCrystalfly"}}},
		rawType,
	},
	"hsr": {
		{Type: "march7th_daily", Label: "三月七日常", Desc: "运行 March7thAssistant 完整日常(清体力/委托/锄大地按其配置)", Fields: []Field{}},
		{Type: "fhoe_route", Label: "Fhoe-Rail 锄大地", Desc: "运行 Fhoe-Rail 预录路线,自动打怪捡垃圾",
			Fields: []Field{{Key: "route", Label: "路线", Type: "text", Placeholder: "留空使用默认路线"}}},
		rawType,
	},
	"wuwa": {
		{Type: "task", Label: "一键任务", Desc: "ok-ww 运行第 N 个任务(一键日常/自动战斗/刷声骸,序号见 ok-ww 界面)",
			Fields: []Field{
				{Key: "task_index", Label: "任务序号 (-t N)", Type: "number", Required: true, Default: 1},
				{Key: "exit", Label: "运行完成后退出 (-e)", Type: "bool", Default: true}}},
		{Type: "farm", Label: "路线刷取(预留)", Desc: "RouteFarmTask 预留接口;ok-ww 支持后可附路线名 (-r)",
			Fields: []Field{
				{Key: "task_index", Label: "任务序号 (-t N)", Type: "number", Required: true, Default: 1},
				{Key: "route", Label: "路线名 (-r)", Type: "text", Placeholder: "可选"},
				{Key: "exit", Label: "运行完成后退出 (-e)", Type: "bool", Default: true}}},
		rawType,
	},
	"r1999": {
		{Type: "run", Label: "运行(默认/指定配置)", Desc: "MaaPiCli 按 M9A 项目配置运行(收荒原/每日心相/常规作战按其配置)",
			Fields: []Field{{Key: "config", Label: "配置名 (-c)", Type: "text", Placeholder: "留空使用默认配置"}}},
		{Type: "config", Label: "指定配置", Desc: "运行 MaaPiCli 的某个已保存配置",
			Fields: []Field{{Key: "config", Label: "配置名 (-c)", Type: "text", Required: true, Placeholder: "如 daily"}}},
		rawType,
	},
}

// Schema returns the UI schema for an adapter key (nil if none defined).
func Schema(adapter string) []TaskTypeInfo { return schemas[adapter] }
