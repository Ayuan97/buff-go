package service

import "paopao-ce/internal/model"

func CreateAttachment(attachment *model.Attachment) (*model.Attachment, error) {
	return myDao.CreateAttachment(attachment)
}
