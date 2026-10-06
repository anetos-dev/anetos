// SPDX-License-Identifier: Apache-2.0

package handlers

import (
	"crypto/rand"
	"encoding/hex"
	"mime"
	"mime/multipart"
	"path"
	"regexp"
	"strings"

	"anetos.dev/anetos/auth/rbac"
	"anetos.dev/anetos/db"
	"anetos.dev/anetos/i18n"
	"anetos.dev/anetos/storage"
	"anetos.dev/anetos/web"

	"anetos.dev/anetos/examples/tracker/app/access"
	"anetos.dev/anetos/examples/tracker/app/models"
)

// AttachInput is a file to attach to an issue: up to 10 MB.
type AttachInput struct {
	Project string                `path:"project"`
	Number  int                   `path:"number"`
	File    *multipart.FileHeader `form:"file" validate:"required|max_size:10MB"`
}

// Attach stores the file on the app's disk (STORAGE_DRIVER) under a
// random name in the project's folder, and records it on the issue.
func (Issues) Attach(c *web.Ctx, in AttachInput) (web.Responder, error) {
	project, issue, err := loadIssue(c, IssuePath{in.Project, in.Number}, access.CreateIssues)
	if err != nil {
		return nil, err
	}
	u, err := currentUser(c)
	if err != nil {
		return nil, err
	}
	disk, err := storage.From(c)
	if err != nil {
		return nil, err
	}
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	p := "attachments/" + project.Key + "/" + hex.EncodeToString(b) + extension(in.File.Filename)
	if err := disk.PutUpload(c, p, in.File); err != nil {
		return nil, err
	}
	info, err := disk.Stat(c, p)
	if err != nil {
		return nil, err
	}
	a := models.Attachment{
		IssueID: issue.ID, UploaderID: u.ID, Name: path.Base(in.File.Filename), Path: p,
		Size: info.Size, ContentType: info.ContentType,
	}
	if err := db.Create(c, &a); err != nil {
		_ = disk.Delete(c, p)
		return nil, err
	}
	c.Session().Flash("status", i18n.T(c, "attachments.status.added", "name", a.Name))
	return web.RedirectRoute("issues.show", project.Key, issue.Number), nil
}

// AttachmentPath is a file of a project: /p/{project}/files/{id}.
type AttachmentPath struct {
	Project string `path:"project"`
	ID      int64  `path:"id"`
}

// loadAttachment returns the project, the attachment and its issue, if
// the attachment is the project's and the user may do p in it.
func loadAttachment(c *web.Ctx, in AttachmentPath, p rbac.Permission) (models.Project, models.Attachment, models.Issue, error) {
	project, err := loadProject(c, in.Project, p)
	if err != nil {
		return project, models.Attachment{}, models.Issue{}, err
	}
	a, err := db.Find[models.Attachment](c, in.ID)
	if err != nil {
		return project, a, models.Issue{}, err
	}
	issue, err := db.Query[models.Issue](c).Where(models.IssueCols.ID.Eq(a.IssueID), models.IssueCols.ProjectID.Eq(project.ID)).First()
	return project, a, issue, err // another project's: db.ErrNotFound
}

// Download sends the file, as an attachment with its name. Files a
// browser would run (HTML, SVG…) are sent so they can't (storage.Serve).
func (Issues) Download(c *web.Ctx, in AttachmentPath) (web.Responder, error) {
	_, a, _, err := loadAttachment(c, in, access.ViewIssues)
	if err != nil {
		return nil, err
	}
	disk, err := storage.From(c)
	if err != nil {
		return nil, err
	}
	return web.ResponderFunc(func(c *web.Ctx) error {
		c.SetHeader("Content-Disposition", mimeAttachment(a.Name))
		disk.Serve(c.Writer(), c.Request(), a.Path)
		return nil
	}), nil
}

// DeleteAttachment removes the file from the issue and the disk: its
// uploader may, and those who may edit the project's issues.
func (Issues) DeleteAttachment(c *web.Ctx, in AttachmentPath) (web.Responder, error) {
	project, a, issue, err := loadAttachment(c, in, access.CreateIssues)
	if err != nil {
		return nil, err
	}
	u, err := currentUser(c)
	if err != nil {
		return nil, err
	}
	if a.UploaderID != u.ID {
		if err := access.Authorize(c, project.Key, access.EditIssues); err != nil {
			return nil, err
		}
	}
	if err := db.Delete(c, &a); err != nil {
		return nil, err
	}
	disk, err := storage.From(c)
	if err != nil {
		return nil, err
	}
	if err := disk.Delete(c, a.Path); err != nil {
		c.Logger().WarnContext(c, "attachment file not deleted", "path", a.Path, "error", err)
	}
	c.Session().Flash("status", i18n.T(c, "attachments.status.deleted", "name", a.Name))
	return web.RedirectRoute("issues.show", project.Key, issue.Number), nil
}

// extension is the file name's extension for the stored file's name
// (its type comes from it): short letters and digits, else none.
func extension(name string) string {
	ext := strings.ToLower(path.Ext(name))
	if !safeExt.MatchString(ext) {
		return ""
	}
	return ext
}

var safeExt = regexp.MustCompile(`^\.[a-z0-9]{1,10}$`)

// mimeAttachment is the Content-Disposition of a download named name.
func mimeAttachment(name string) string {
	if v := mime.FormatMediaType("attachment", map[string]string{"filename": name}); v != "" {
		return v
	}
	return "attachment"
}
