/*
   GoToSocial
   Copyright (C) 2021-2023 GoToSocial Authors admin@gotosocial.org

   This program is free software: you can redistribute it and/or modify
   it under the terms of the GNU Affero General Public License as published by
   the Free Software Foundation, either version 3 of the License, or
   (at your option) any later version.

   This program is distributed in the hope that it will be useful,
   but WITHOUT ANY WARRANTY; without even the implied warranty of
   MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
   GNU Affero General Public License for more details.

   You should have received a copy of the GNU Affero General Public License
   along with this program.  If not, see <http://www.gnu.org/licenses/>.
*/

package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"

	apimodel "github.com/superseriousbusiness/gotosocial/internal/api/model"
	"github.com/superseriousbusiness/gotosocial/internal/gtserror"
	"github.com/superseriousbusiness/gotosocial/internal/gtsmodel"
)

func readDomains(domains *multipart.FileHeader) ([]*apimodel.DomainBlock, error) {
	f, err := domains.Open()
	if err != nil {
		return nil, err
	}
	defer f.Close()

	buf := new(bytes.Buffer)
	size, err := io.Copy(buf, f)
	if err != nil {
		return nil, err
	}

	if size == 0 {
		return nil, errors.New("size 0 bytes")
	}

	d := []*apimodel.DomainBlock{}
	if err := json.Unmarshal(buf.Bytes(), &d); err != nil {
		return nil, err
	}

	return d, nil
}

// DomainBlocksImport handles the import of a bunch of domain blocks at once, by calling the DomainBlockCreate function for each domain in the provided file.
func (p *processor) DomainBlocksImport(ctx context.Context, account *gtsmodel.Account, domains *multipart.FileHeader) ([]*apimodel.DomainBlock, gtserror.WithCode) {
	raw, err := readDomains(domains)
	if err != nil {
		return nil, gtserror.NewErrorBadRequest(fmt.Errorf("DomainBlocksImport: could not read provided attachment: %w", err))
	}

	normalized, errs := normalizeDomainBlocks(raw)

	blocks := make([]*apimodel.DomainBlock, 0, len(normalized))
	for _, d := range normalized {
		block, err := p.DomainBlockCreate(ctx, account, d.Domain.Domain, false, d.PublicComment, "", "")
		if err != nil {
			return nil, err
		}

		blocks = append(blocks, block)
	}

	return blocks, nil
}
