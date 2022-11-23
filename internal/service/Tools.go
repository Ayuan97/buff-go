package service

import "fmt"

// 更新比例
func UpdateGoodsProportion(goodsId int) {

	goodsInfo, err := myDao.GetGoodsByGoodsId(int64(goodsId))
	if err != nil {
		fmt.Println("UpdateGoodsProportion err :", err)
		return
	}
	if goodsInfo.GoodsId > 0 {
		goodsInfo.Proportion = goodsInfo.BuyMaxPrice / goodsInfo.SteamSellPrice
		err := myDao.UpdateGoodsRatio(goodsInfo, goodsInfo.Proportion)
		if err != nil {
			fmt.Println("更新比例失败", err)
			return
		}
	}

}
