package clicktext

import "strings"

// Chars is the glyph set baked into assets/glyphs.png by tools/glyphatlas.
// Common characters only, with look-alikes removed so people are never asked
// to tell 己/已/巳, 土/士, 未/末, 人/入, 日/曰, 大/太/犬/天, 王/玉 or 午/牛 apart,
// and no stroke-only characters (一, 十, 口) that read the same when rotated.
// Changing this string requires regenerating the atlas.
const Chars = baseChars + idiomChars

const baseChars = "" +
	"春夏秋冬风花雪月山河海江湖云雨雷电星光明晴阳林森树叶草竹松梅兰菊莲桃李杏果米茶酒饭面菜鱼鸟龙" +
	"虎马羊猪狗猫鸡鸭鹅熊鹿象猴兔蛇城街路桥楼房屋门窗灯桌椅床船车飞机站港书笔纸墨画琴棋诗词歌舞戏" +
	"剧乐红黄蓝绿紫黑灰金银铜铁东南西北左右前后爱情心思想梦笑哭喜怒悲欢家国民族友朋兄弟姐妹爸妈孙" +
	"学校课读写算考题答问走跑跳游泳坐睡醒早晚夜晨快慢高低长短新旧好坏美丑多少远近深浅轻重冷热暖凉" +
	"开关进出来去回到看听说唱福寿财富贵吉祥安康平珠节年岁周期钟表钱包票卡衣帽鞋袜裙眼耳鼻嘴手脚头" +
	"发医药病院厨洗澡刷牙梳镜伞扇盆碗筷勺杯瓶壶锅盘糖盐油醋酱蛋肉鸽燕雁鹰蝶蜂蚁虫蛙龟鲸鲨贝壳沙滩" +
	"岛湾峰岭谷溪泉瀑池塘田园村镇省县乡野场厂店铺商超购物价数字语言汉英法德俄韩泰越印度澳洲欧非亚" +
	"球赛步拳剑弓箭枪炮兵将帅相仕"

// idiomChars extends the set with the characters click_order idioms need.
const idiomChars = "" +
	"万业中为之争事五亡今以似假先公兴军凤击分刻力功助勤千半华协古同和四因圆壮声外夺威守实尔尾居巧废张" +
	"影待得怡恐惊意成扬拔指掩放故文无时旷是暗更有望柳株气水求波流涯添清温源澜炭点烈熟爽牢狐生用百盗盼" +
	"睛知石神秀秒立第繁细群而胸能腔致舟苗莺落蝉补调足送逐途采铃锦阔雅青顺顾颜饮香鸣鹤齐"

// Idioms are four-character idioms for click_order: common, four distinct
// characters, none of them a look-alike excluded above.
var Idioms = strings.Fields("" +
	"春暖花开 国泰民安 鸟语花香 风和雨顺 万紫千红 百花齐放 山清水秀 车水马龙 水落石出 花好月圆 实事求是 画龙点睛 " +
	"守株待兔 亡羊补牢 刻舟求剑 拔苗助长 半途而废 心想事成 马到成功 安居乐业 同心协力 学以致用 东张西望 五湖四海 " +
	"千军万马 风调雨顺 古今中外 雪中送炭 杯弓蛇影 画蛇添足 掩耳盗铃 狐假虎威 惊弓之鸟 指鹿为马 叶公好龙 胸有成竹 " +
	"熟能生巧 温故知新 春华秋实 秋高气爽 冬暖夏凉 鸟语蝉鸣 金秋时节 青山绿水 桃红柳绿 柳暗花明 莺歌燕舞 龙飞凤舞 " +
	"虎头蛇尾 鹤立鸡群 声东击西 南腔北调 前因后果 左顾右盼 喜笑颜开 笑逐颜开 欢声笑语 风花雪月 诗情画意 琴棋书画 " +
	"书香门第 学海无涯 勤学好问 温文尔雅 争分夺秒 争先恐后 兴高采烈 心旷神怡 神采飞扬 和风细雨 细水长流 源远流长 " +
	"饮水思源 高山流水 波澜壮阔 春风得意 万象更新 繁花似锦 ")
