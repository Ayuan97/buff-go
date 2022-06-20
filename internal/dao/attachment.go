package dao

import "paopao-ce/internal/model"

func (d *Dao) CreateAttachment(attachment *model.Attachment) (*model.Attachment, error) {
	return attachment.Create(d.engine)
}
