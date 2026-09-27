package fs

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/sharedcode/joltrin/v5/encoding"
)

// CopyToPassiveFolders copies store metadata (store list and per-store info) and registry
// segment files from the active folder to passive targets. It temporarily flips the active
// folder toggler to write into the passive side via the fileIO replication wrapper.
func (sr *StoreRepository) CopyToPassiveFolders(ctx context.Context) error {
	// Copy StoreRepositories to passive targets.
	// Copy Registries to passive targets.

	if storeList, err := sr.GetAll(ctx); err != nil {
		return err
	} else {

		oaf := sr.replicationTracker.ReplicationTrackedDetails.ActiveFolderToggler
		sr.replicationTracker.ReplicationTrackedDetails.ActiveFolderToggler = !oaf
		storeWriter := newFileIOWithReplication(sr.replicationTracker, sr.manageStore, false)

		// Restore the active folder toggler value upon out of scope.
		defer func() {
			sr.replicationTracker.ReplicationTrackedDetails.ActiveFolderToggler = oaf
		}()

		// Write the store list.
		ba, _ := encoding.Marshal(storeList)
		if err := storeWriter.createStore(ctx, ""); err != nil {
			return err
		}
		if err := storeWriter.write(ctx, storeListFilename, ba); err != nil {
			return err
		}

		for _, storeName := range storeList {

			// Create the Store folder.
			if err := storeWriter.createStore(ctx, storeName); err != nil {
				return err
			}

			store, err := sr.Get(ctx, storeName)
			if err != nil {
				return err
			}
			if len(store) == 0 {
				// Store might have been deleted concurrently, skip it.
				continue
			}
			// Write the store info.
			ba, err := encoding.Marshal(store[0])
			if err != nil {
				return err
			}
			if err := storeWriter.write(ctx, fmt.Sprintf("%c%s%c%s", os.PathSeparator, storeName, os.PathSeparator, StoreInfoFilename), ba); err != nil {
				return err
			}

			// Copy this store's registry segment file(s).
			// Important: we temporarily flipped ActiveFolderToggler above to write store list/info
			// into the passive side. For copying registry segments we must use the ORIGINAL
			// active/passive mapping captured in 'oaf' so we copy from original active -> original passive.
			var srcDir, dstDir string
			// Registry segments live under the registry table folder (e.g., "c1_r"), not under the store name.
			tableName := store[0].RegistryTable
			if oaf {
				// Original active is storesBaseFolders[0], passive is [1]
				srcDir = filepath.Join(sr.replicationTracker.storesBaseFolders[0], tableName)
				dstDir = filepath.Join(sr.replicationTracker.storesBaseFolders[1], tableName)
			} else {
				// Original active is storesBaseFolders[1], passive is [0]
				srcDir = filepath.Join(sr.replicationTracker.storesBaseFolders[1], tableName)
				dstDir = filepath.Join(sr.replicationTracker.storesBaseFolders[0], tableName)
			}
			if err := copyFilesByExtension(ctx, srcDir, dstDir, registryFileExtension); err != nil {
				return err
			}
		}
	}

	return nil
}

// copyFilesByExtension copies files with the given extension from sourceDir to targetDir.
func copyFilesByExtension(ctx context.Context, sourceDir, targetDir, extension string) error {
	fio := NewFileIO()
	files, err := fio.ReadDir(ctx, sourceDir)
	if err != nil {
		return fmt.Errorf("error reading source directory: %w", err)
	}

	// Ensure destination directory exists to avoid create errors.
	if err := fio.MkdirAll(ctx, targetDir, 0o755); err != nil {
		return fmt.Errorf("error creating target directory: %w", err)
	}

	for _, file := range files {
		if file.IsDir() {
			continue // Skip directories
		}

		if strings.HasSuffix(file.Name(), extension) {
			sourcePath := filepath.Join(sourceDir, file.Name())
			targetPath := filepath.Join(targetDir, file.Name())

			if err := copyFile(sourcePath, targetPath); err != nil {
				return fmt.Errorf("error copying file %s: %w", file.Name(), err)
			}
		}
	}

	return nil
}

// copyFile streams bytes from sourcePath to targetPath using io.Copy.
func copyFile(sourcePath, targetPath string) error {
	sourceFile, err := os.Open(sourcePath)
	if err != nil {
		return fmt.Errorf("error opening source file: %w", err)
	}
	defer sourceFile.Close()

	targetFile, err := os.Create(targetPath)
	if err != nil {
		return fmt.Errorf("error creating target file: %w", err)
	}
	defer targetFile.Close()

	_, err = io.Copy(targetFile, sourceFile)
	if err != nil {
		return fmt.Errorf("error copying file content: %w", err)
	}

	return nil
}
