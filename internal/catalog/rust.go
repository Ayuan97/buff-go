package catalog

import (
	"sort"
	"strings"
)

// AppIDRust is Steam's Rust appid.
const AppIDRust int64 = 252490

// rustWorkshopType 是 Rust 搜索结果里 asset_description.type 的实测值，不能当分类用。
const rustWorkshopType = "创意工坊物品"

// RustCategory 是 Steam 市场 category_steamcat 的一项。
// Label 是 Steam 原文，采集仍只传 Slug；Name 只给控制台展示。
type RustCategory struct {
	Slug  string `json:"slug"`
	Label string `json:"label"`
	Name  string `json:"name"`
}

// RustItemClass 是 Steam 市场 category_itemclass 的一项。
// Label 用于按商品名回填 item_type，采集仍只传 Slug；Name 只给控制台展示。
type RustItemClass struct {
	Slug     string `json:"slug"`
	Label    string `json:"label"`
	Name     string `json:"name"`
	Category string `json:"category"`
}

// RustCategories 来自 Steam Rust 市场筛选。slug 原样传给 search/render。
func RustCategories() []RustCategory {
	return []RustCategory{
		{Slug: "steamcat.armor", Label: "Armor", Name: "护甲"},
		{Slug: "steamcat.clothing", Label: "Clothing", Name: "服装"},
		{Slug: "steamcat.misc", Label: "Misc", Name: "杂项"},
		{Slug: "steamcat.resource", Label: "Resource", Name: "资源"},
		{Slug: "steamcat.weapon", Label: "Weapon", Name: "武器"},
	}
}

// RustItemClasses 来自 Steam Rust 市场 item class 勾选列表。
// Category 只给页面分组用；Steam 自己按 slug 过滤，不依赖这列。
func RustItemClasses() []RustItemClass {
	return []RustItemClass{
		{Slug: "ak47u", Label: "AK47u", Name: "定制步枪", Category: "steamcat.weapon"},
		{Slug: "armored.metal.door", Label: "Armored Metal Door", Name: "装甲金属门", Category: "steamcat.misc"},
		{Slug: "balaclava", Label: "Balaclava", Name: "蒙面头巾", Category: "steamcat.clothing"},
		{Slug: "bandana", Label: "Bandana", Name: "头巾", Category: "steamcat.clothing"},
		{Slug: "beenie", Label: "Beenie", Name: "毛线帽", Category: "steamcat.clothing"},
		{Slug: "bolt.rifle", Label: "Bolt Rifle", Name: "栓动步枪", Category: "steamcat.weapon"},
		{Slug: "bone.club", Label: "Bone Club", Name: "骨棒", Category: "steamcat.weapon"},
		{Slug: "bone.knife", Label: "Bone Knife", Name: "骨刀", Category: "steamcat.weapon"},
		{Slug: "boonie", Label: "Boonie", Name: "奔尼帽", Category: "steamcat.clothing"},
		{Slug: "boots", Label: "Boots", Name: "靴子", Category: "steamcat.clothing"},
		{Slug: "bucket.helmet", Label: "Bucket Helmet", Name: "铁桶盔", Category: "steamcat.armor"},
		{Slug: "burlap.gloves", Label: "Burlap Gloves", Name: "粗布手套", Category: "steamcat.clothing"},
		{Slug: "burlap.headwrap", Label: "Burlap Headwrap", Name: "粗布头巾", Category: "steamcat.clothing"},
		{Slug: "burlap.shirt", Label: "Burlap Shirt", Name: "粗布衬衫", Category: "steamcat.clothing"},
		{Slug: "burlap.shoes", Label: "Burlap Shoes", Name: "粗布鞋", Category: "steamcat.clothing"},
		{Slug: "burlap.trousers", Label: "Burlap Trousers", Name: "粗布裤", Category: "steamcat.clothing"},
		{Slug: "cap", Label: "Cap", Name: "鸭舌帽", Category: "steamcat.clothing"},
		{Slug: "coffeecan.helmet", Label: "Coffeecan Helmet", Name: "咖啡罐盔", Category: "steamcat.armor"},
		{Slug: "collared.shirt", Label: "Collared Shirt", Name: "有领衬衫", Category: "steamcat.clothing"},
		{Slug: "concrete.barricade", Label: "Concrete Barricade", Name: "混凝土路障", Category: "steamcat.misc"},
		{Slug: "crossbow", Label: "Crossbow", Name: "弩", Category: "steamcat.weapon"},
		{Slug: "deer.skull.mask", Label: "Deer Skull Mask", Name: "鹿骨面具", Category: "steamcat.armor"},
		{Slug: "double.barrel.shotgun", Label: "Double Barrel Shotgun", Name: "双管霰弹枪", Category: "steamcat.weapon"},
		{Slug: "grenade", Label: "Grenade", Name: "手雷", Category: "steamcat.weapon"},
		{Slug: "guitar", Label: "Guitar", Name: "吉他", Category: "steamcat.misc"},
		{Slug: "hammer", Label: "Hammer", Name: "锤子", Category: "steamcat.weapon"},
		{Slug: "hatchet", Label: "Hatchet", Name: "斧头", Category: "steamcat.weapon"},
		{Slug: "hide.halterneck", Label: "Hide Halterneck", Name: "兽皮吊带", Category: "steamcat.clothing"},
		{Slug: "hide.pants", Label: "Hide Pants", Name: "兽皮裤", Category: "steamcat.clothing"},
		{Slug: "hide.poncho", Label: "Hide Poncho", Name: "兽皮披风", Category: "steamcat.clothing"},
		{Slug: "hide.skirt", Label: "Hide Skirt", Name: "兽皮裙", Category: "steamcat.clothing"},
		{Slug: "hoodie", Label: "Hoodie", Name: "连帽衫", Category: "steamcat.clothing"},
		{Slug: "jacket", Label: "Jacket", Name: "夹克", Category: "steamcat.clothing"},
		{Slug: "large.wooden.box", Label: "Large Wooden Box", Name: "大木箱", Category: "steamcat.misc"},
		{Slug: "long.tshirt", Label: "Long TShirt", Name: "长袖T恤", Category: "steamcat.clothing"},
		{Slug: "longsword", Label: "Longsword", Name: "长剑", Category: "steamcat.weapon"},
		{Slug: "metal.facemask", Label: "Metal Facemask", Name: "金属面罩", Category: "steamcat.armor"},
		{Slug: "metal.torso.plate", Label: "Metal Torso Plate", Name: "金属胸甲", Category: "steamcat.armor"},
		{Slug: "miners.hat", Label: "Miner's Hat", Name: "矿工帽", Category: "steamcat.clothing"},
		{Slug: "mp5", Label: "Mp5", Name: "MP5", Category: "steamcat.weapon"},
		{Slug: "pants", Label: "Pants", Name: "裤子", Category: "steamcat.clothing"},
		{Slug: "pump.shotgun", Label: "Pump Shotgun", Name: "泵动霰弹枪", Category: "steamcat.weapon"},
		{Slug: "reactive.sign", Label: "Reactive Sign", Name: "电子指示牌", Category: "steamcat.misc"},
		{Slug: "revolver", Label: "Revolver", Name: "左轮手枪", Category: "steamcat.weapon"},
		{Slug: "rifle.helmet", Label: "Rifle Helmet", Name: "步枪头盔", Category: "steamcat.armor"},
		{Slug: "roadsign.jacket", Label: "Roadsign Jacket", Name: "路牌甲", Category: "steamcat.armor"},
		{Slug: "roadsign.kilt", Label: "Roadsign Kilt", Name: "路牌裙甲", Category: "steamcat.armor"},
		{Slug: "rock", Label: "Rock", Name: "石头", Category: "steamcat.weapon"},
		{Slug: "rocket.launcher", Label: "Rocket Launcher", Name: "火箭筒", Category: "steamcat.weapon"},
		{Slug: "salvaged.icepick", Label: "Salvaged Icepick", Name: "回收冰镐", Category: "steamcat.weapon"},
		{Slug: "salvaged.sword", Label: "Salvaged Sword", Name: "回收剑", Category: "steamcat.weapon"},
		{Slug: "sandbag.barricade", Label: "Sandbag Barricade", Name: "沙袋路障", Category: "steamcat.misc"},
		{Slug: "satchel.explosives", Label: "Satchel Explosives", Name: "炸药包", Category: "steamcat.weapon"},
		{Slug: "semi.auto.pistol", Label: "Semi Auto Pistol", Name: "半自动手枪", Category: "steamcat.weapon"},
		{Slug: "semi.auto.rifle", Label: "Semi Auto Rifle", Name: "半自动步枪", Category: "steamcat.weapon"},
		{Slug: "sheet.metal.door", Label: "Sheet Metal Door", Name: "铁皮门", Category: "steamcat.misc"},
		{Slug: "shorts", Label: "Shorts", Name: "短裤", Category: "steamcat.clothing"},
		{Slug: "sleeping.bag", Label: "Sleeping Bag", Name: "睡袋", Category: "steamcat.misc"},
		{Slug: "smg", Label: "SMG", Name: "自制冲锋枪", Category: "steamcat.weapon"},
		{Slug: "snow.jacket", Label: "Snow Jacket", Name: "雪地夹克", Category: "steamcat.clothing"},
		{Slug: "stone.hatchet", Label: "Stone Hatchet", Name: "石斧", Category: "steamcat.weapon"},
		{Slug: "stone.pickaxe", Label: "Stone Pickaxe", Name: "石镐", Category: "steamcat.weapon"},
		{Slug: "tank.top", Label: "Tank Top", Name: "背心", Category: "steamcat.clothing"},
		{Slug: "thompson", Label: "Thompson", Name: "汤姆逊", Category: "steamcat.weapon"},
		{Slug: "tshirt", Label: "TShirt", Name: "T恤", Category: "steamcat.clothing"},
		{Slug: "waterpipe.shotgun", Label: "Waterpipe Shotgun", Name: "水管喷", Category: "steamcat.weapon"},
		{Slug: "wooden.box", Label: "Wooden Box", Name: "木箱", Category: "steamcat.misc"},
		{Slug: "wooden.door", Label: "Wooden Door", Name: "木门", Category: "steamcat.misc"},
	}
}

// ValidRustCategory 判断是不是词表里的 steamcat。
func ValidRustCategory(slug string) bool {
	for _, category := range RustCategories() {
		if category.Slug == slug {
			return true
		}
	}
	return false
}

// ValidRustItemClass 判断是不是词表里的 itemclass。
func ValidRustItemClass(slug string) bool {
	for _, class := range RustItemClasses() {
		if class.Slug == slug {
			return true
		}
	}
	return false
}

// RustLabelsForCategories 返回这些 steamcat 下的 item class 英文标签，供行情 SQL IN 使用。
func RustLabelsForCategories(slugs []string) []string {
	wanted := make(map[string]struct{}, len(slugs))
	for _, slug := range slugs {
		wanted[slug] = struct{}{}
	}
	labels := make([]string, 0)
	seen := make(map[string]struct{})
	for _, class := range RustItemClasses() {
		if _, ok := wanted[class.Category]; !ok {
			continue
		}
		if _, dup := seen[class.Label]; dup {
			continue
		}
		seen[class.Label] = struct{}{}
		labels = append(labels, class.Label)
	}
	return labels
}

// RustLabelForClass 把 itemclass slug 收成英文标签。
func RustLabelForClass(slug string) (string, bool) {
	for _, class := range RustItemClasses() {
		if class.Slug == slug {
			return class.Label, true
		}
	}
	return "", false
}

// IsRustWorkshopType 是 Steam 给 Rust 的无用 type，不能当筛选值留下。
func IsRustWorkshopType(value string) bool {
	return strings.TrimSpace(value) == rustWorkshopType
}

// MatchRustItemClass 用商品名里最长的英文 item class 标签认分类。认不出就空着。
func MatchRustItemClass(name string) (RustItemClass, bool) {
	name = strings.TrimSpace(name)
	if name == "" {
		return RustItemClass{}, false
	}
	folded := strings.ToLower(name)
	classes := append([]RustItemClass(nil), RustItemClasses()...)
	sort.SliceStable(classes, func(i, j int) bool {
		return len(classes[i].Label) > len(classes[j].Label)
	})
	for _, class := range classes {
		if strings.Contains(folded, strings.ToLower(class.Label)) {
			return class, true
		}
	}
	return RustItemClass{}, false
}
