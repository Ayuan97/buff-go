package service

import "qingshanyoufeng/internal/model"

func CreateAttachment(attachment *model.Attachment) (*model.Attachment, error) {
	return myDao.CreateAttachment(attachment)
}
