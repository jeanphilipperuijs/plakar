/*
 * Copyright (c) 2021 Gilles Chehade <gilles@poolp.org>
 *
 * Permission to use, copy, modify, and distribute this software for any
 * purpose with or without fee is hereby granted, provided that the above
 * copyright notice and this permission notice appear in all copies.
 *
 * THE SOFTWARE IS PROVIDED "AS IS" AND THE AUTHOR DISCLAIMS ALL WARRANTIES
 * WITH REGARD TO THIS SOFTWARE INCLUDING ALL IMPLIED WARRANTIES OF
 * MERCHANTABILITY AND FITNESS. IN NO EVENT SHALL THE AUTHOR BE LIABLE FOR
 * ANY SPECIAL, DIRECT, INDIRECT, OR CONSEQUENTIAL DAMAGES OR ANY DAMAGES
 * WHATSOEVER RESULTING FROM LOSS OF USE, DATA OR PROFITS, WHETHER IN AN
 * ACTION OF CONTRACT, NEGLIGENCE OR OTHER TORTIOUS ACTION, ARISING OUT OF
 * OR IN CONNECTION WITH THE USE OR PERFORMANCE OF THIS SOFTWARE.
 */

package utils

import (
	"sort"
	"sync"
	"time"

	"github.com/PlakarKorp/plakar/objects"
	"github.com/PlakarKorp/plakar/repository"
	"github.com/PlakarKorp/plakar/snapshot"
)

type locateSortOrder int

const (
	LocateSortOrderNone       locateSortOrder = 0
	LocateSortOrderAscending  locateSortOrder = 1
	LocateSortOrderDescending locateSortOrder = -1
)

type LocateOptions struct {
	MaxConcurrency int
	SortOrder      locateSortOrder
	Latest         bool

	Before time.Time
	Since  time.Time

	Name        string
	Category    string
	Environment string
	Perimeter   string
	Job         string
	Tag         string
}

func NewDefaultLocateOptions() *LocateOptions {
	return &LocateOptions{
		MaxConcurrency: 1,
		SortOrder:      LocateSortOrderNone,
		Latest:         false,

		Before: time.Time{},
		Since:  time.Time{},

		Name:        "",
		Category:    "",
		Environment: "",
		Perimeter:   "",
		Job:         "",
		Tag:         "",
	}
}

func LocateSnapshotIDs(repo *repository.Repository, opts *LocateOptions) ([]objects.MAC, error) {
	type result struct {
		snapshotID objects.MAC
		timestamp  time.Time
	}

	workSet := make([]result, 0)
	workSetMutex := sync.Mutex{}

	if opts == nil {
		opts = NewDefaultLocateOptions()
	}

	wg := sync.WaitGroup{}
	maxConcurrency := make(chan struct{}, opts.MaxConcurrency)
	for snapshotID := range repo.ListSnapshots() {
		maxConcurrency <- struct{}{}
		wg.Add(1)
		go func(snapshotID objects.MAC) {
			defer func() {
				<-maxConcurrency
				wg.Done()
			}()

			snap, err := snapshot.Load(repo, snapshotID)
			if err != nil {
				return
			}
			defer snap.Close()

			if opts.Name != "" {
				if snap.Header.Name != opts.Name {
					return
				}
			}

			if opts.Category != "" {
				if snap.Header.Category != opts.Category {
					return
				}
			}

			if opts.Environment != "" {
				if snap.Header.Environment != opts.Environment {
					return
				}
			}

			if opts.Perimeter != "" {
				if snap.Header.Perimeter != opts.Perimeter {
					return
				}
			}

			if opts.Job != "" {
				if snap.Header.Job != opts.Job {
					return
				}
			}

			if opts.Tag != "" {
				if !snap.Header.HasTag(opts.Tag) {
					return
				}
			}

			if !opts.Before.IsZero() {
				if snap.Header.Timestamp.After(opts.Before) {
					return
				}
			}

			if !opts.Since.IsZero() {
				if snap.Header.Timestamp.Before(opts.Since) {
					return
				}
			}

			workSetMutex.Lock()
			workSet = append(workSet, result{
				snapshotID: snapshotID,
				timestamp:  snap.Header.Timestamp,
			})
			workSetMutex.Unlock()
		}(snapshotID)
	}
	wg.Wait()

	if opts.SortOrder != LocateSortOrderNone {
		if opts.SortOrder == LocateSortOrderAscending {
			sort.SliceStable(workSet, func(i, j int) bool {
				return workSet[i].timestamp.Before(workSet[j].timestamp)
			})
		} else {
			sort.SliceStable(workSet, func(i, j int) bool {
				return workSet[i].timestamp.After(workSet[j].timestamp)
			})
		}
	}

	if opts.Latest {
		if len(workSet) > 1 {
			workSet = workSet[len(workSet)-1:]
		}
	}

	resultSet := make([]objects.MAC, 0, len(workSet))
	for _, result := range workSet {
		resultSet = append(resultSet, result.snapshotID)
	}

	return resultSet, nil
}
