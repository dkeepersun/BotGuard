package clicktext

// Chars is the glyph set baked into assets/glyphs.png by tools/glyphatlas.
// Common characters only, with look-alikes removed so people are never asked
// to tell 己/已/巳, 土/士, 未/末, 人/入, 日/曰, 大/太/犬/天, 王/玉 or 午/牛 apart,
// and no stroke-only characters (一, 十, 口) that read the same when rotated.
// Changing this string requires regenerating the atlas.
const Chars = "" +
	"春夏秋冬风花雪月山河海江湖云雨雷电星光明晴阳林森树叶草竹松梅兰菊莲桃李杏果米茶酒饭面菜鱼鸟龙" +
	"虎马羊猪狗猫鸡鸭鹅熊鹿象猴兔蛇城街路桥楼房屋门窗灯桌椅床船车飞机站港书笔纸墨画琴棋诗词歌舞戏" +
	"剧乐红黄蓝绿紫黑灰金银铜铁东南西北左右前后爱情心思想梦笑哭喜怒悲欢家国民族友朋兄弟姐妹爸妈孙" +
	"学校课读写算考题答问走跑跳游泳坐睡醒早晚夜晨快慢高低长短新旧好坏美丑多少远近深浅轻重冷热暖凉" +
	"开关进出来去回到看听说唱福寿财富贵吉祥安康平珠节年岁周期钟表钱包票卡衣帽鞋袜裙眼耳鼻嘴手脚头" +
	"发医药病院厨洗澡刷牙梳镜伞扇盆碗筷勺杯瓶壶锅盘糖盐油醋酱蛋肉鸽燕雁鹰蝶蜂蚁虫蛙龟鲸鲨贝壳沙滩" +
	"岛湾峰岭谷溪泉瀑池塘田园村镇省县乡野场厂店铺商超购物价数字语言汉英法德俄韩泰越印度澳洲欧非亚" +
	"球赛步拳剑弓箭枪炮兵将帅相仕"
