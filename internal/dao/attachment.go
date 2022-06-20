package dao

import "qingshanyoufeng/internal/model"

func (d *Dao) CreateAttachment(attachment *model.Attachment) (*model.Attachment, error) {
	return attachment.Create(d.engine)
}
