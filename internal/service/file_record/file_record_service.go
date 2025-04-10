/*
 * Licensed to the Apache Software Foundation (ASF) under one
 * or more contributor license agreements.  See the NOTICE file
 * distributed with this work for additional information
 * regarding copyright ownership.  The ASF licenses this file
 * to you under the Apache License, Version 2.0 (the
 * "License"); you may not use this file except in compliance
 * with the License.  You may obtain a copy of the License at
 *
 *   http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing,
 * software distributed under the License is distributed on an
 * "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY
 * KIND, either express or implied.  See the License for the
 * specific language governing permissions and limitations
 * under the License.
 */

package file_record

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/apache/answer/internal/base/constant"
	"github.com/apache/answer/internal/entity"
	"github.com/apache/answer/internal/service/revision"
	"github.com/apache/answer/internal/service/service_config"
	"github.com/apache/answer/internal/service/siteinfo_common"
	"github.com/apache/answer/pkg/checker"
	"github.com/apache/answer/pkg/dir"
	"github.com/apache/answer/pkg/writer"
	"github.com/segmentfault/pacman/log"
)

// FileRecordRepo file record repository
type FileRecordRepo interface {
	AddFileRecord(ctx context.Context, fileRecord *entity.FileRecord) (err error)
	UpdateFileRecord(ctx context.Context, fileRecord *entity.FileRecord) (err error)
	GetFileRecordPage(ctx context.Context, page, pageSize int, cond *entity.FileRecord) (
		fileRecordList []*entity.FileRecord, total int64, err error)
	DeleteFileRecord(ctx context.Context, id int) (err error)
}

// FileRecordService file record service
type FileRecordService struct {
	fileRecordRepo  FileRecordRepo
	revisionRepo    revision.RevisionRepo
	serviceConfig   *service_config.ServiceConfig
	siteInfoService siteinfo_common.SiteInfoCommonService
}

// NewFileRecordService new file record service
func NewFileRecordService(
	fileRecordRepo FileRecordRepo,
	revisionRepo revision.RevisionRepo,
	serviceConfig *service_config.ServiceConfig,
	siteInfoService siteinfo_common.SiteInfoCommonService,
) *FileRecordService {
	return &FileRecordService{
		fileRecordRepo:  fileRecordRepo,
		revisionRepo:    revisionRepo,
		serviceConfig:   serviceConfig,
		siteInfoService: siteInfoService,
	}
}

// AddFileRecord add file record
func (fs *FileRecordService) AddFileRecord(ctx context.Context, userID, filePath, fileURL, source string) {
	record := &entity.FileRecord{
		UserID:   userID,
		FilePath: filePath,
		FileURL:  fileURL,
		Source:   source,
		Status:   entity.FileRecordStatusAvailable,
		ObjectID: "0",
	}
	if err := fs.fileRecordRepo.AddFileRecord(ctx, record); err != nil {
		log.Errorf("add file record error: %v", err)
	}
}

// CleanOrphanUploadFiles clean orphan upload files
func (fs *FileRecordService) CleanOrphanUploadFiles(ctx context.Context) {
	page, pageSize := 1, 1000

	for {
		fileRecordList, total, err := fs.fileRecordRepo.GetFileRecordPage(ctx, page, pageSize, &entity.FileRecord{
			Status: entity.FileRecordStatusAvailable,
		})
		if err != nil {
			log.Errorf("get file record page error: %v", err)
			return
		}
		if len(fileRecordList) == 0 || total == 0 {
			break
		}
		for _, fileRecord := range fileRecordList {
			// If this file record created in 48 hours, no need to check
			if fileRecord.CreatedAt.AddDate(0, 0, 2).After(time.Now()) {
				continue
			}
			if checker.IsNotZeroString(fileRecord.ObjectID) {
				_, exist, err := fs.revisionRepo.GetLastRevisionByObjectID(ctx, fileRecord.ObjectID)
				if err != nil {
					log.Errorf("get last revision by object id error: %v", err)
					continue
				}
				if exist {
					continue
				}
			} else {
				lastRevision, exist, err := fs.revisionRepo.GetLastRevisionByFileURL(ctx, fileRecord.FileURL)
				if err != nil {
					log.Errorf("get last revision by file url error: %v", err)
					continue
				}
				if exist {
					// update the file record object id
					fileRecord.ObjectID = lastRevision.ObjectID
					if err := fs.fileRecordRepo.UpdateFileRecord(ctx, fileRecord); err != nil {
						log.Errorf("update file record object id error: %v", err)
					}
					continue
				}
			}
			// Delete and move the file record
			if err := fs.deleteAndMoveFileRecord(ctx, fileRecord); err != nil {
				log.Error(err)
			}
		}
		page++
	}
}

func (fs *FileRecordService) PurgeDeletedFiles(ctx context.Context) {
	deletedPath := filepath.Join(fs.serviceConfig.UploadPath, constant.DeletedSubPath)
	log.Infof("purge deleted files: %s", deletedPath)
	err := os.RemoveAll(deletedPath)
	if err != nil {
		log.Errorf("purge deleted files error: %v", err)
		return
	}
	err = dir.CreateDirIfNotExist(deletedPath)
	if err != nil {
		log.Errorf("create deleted directory error: %v", err)
	}
	return
}

func (fs *FileRecordService) deleteAndMoveFileRecord(ctx context.Context, fileRecord *entity.FileRecord) error {
	// Delete the file record
	if err := fs.fileRecordRepo.DeleteFileRecord(ctx, fileRecord.ID); err != nil {
		return fmt.Errorf("delete file record error: %v", err)
	}

	// Move the file to the deleted directory
	oldFilename := filepath.Base(fileRecord.FilePath)
	oldFilePath := filepath.Join(fs.serviceConfig.UploadPath, fileRecord.FilePath)
	deletedPath := filepath.Join(fs.serviceConfig.UploadPath, constant.DeletedSubPath, oldFilename)

	if err := writer.MoveFile(oldFilePath, deletedPath); err != nil {
		return fmt.Errorf("move file error: %v", err)
	}

	log.Debugf("delete and move file: %s", fileRecord.FileURL)
	return nil
}
