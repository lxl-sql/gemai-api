/*
Copyright (C) 2025-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/

import React from 'react';
import CardPro from '../../common/ui/CardPro';
import QuotaTransactionsTable from './QuotaTransactionsTable';
import QuotaTransactionsFilters from './QuotaTransactionsFilters';
import QuotaTransactionsDescription from './QuotaTransactionsDescription';
import { useQuotaTransactionsData } from '../../../hooks/quota-transactions/useQuotaTransactionsData';
import { useIsMobile } from '../../../hooks/common/useIsMobile';
import { createCardProPagination } from '../../../helpers/utils';

const QuotaTransactionsPage = () => {
  const transactionsData = useQuotaTransactionsData();
  const isMobile = useIsMobile();

  return (
    <CardPro
      type='type1'
      descriptionArea={
        <QuotaTransactionsDescription
          compactMode={transactionsData.compactMode}
          setCompactMode={transactionsData.setCompactMode}
          t={transactionsData.t}
        />
      }
      searchArea={<QuotaTransactionsFilters {...transactionsData} />}
      paginationArea={createCardProPagination({
        currentPage: transactionsData.activePage,
        pageSize: transactionsData.pageSize,
        total: transactionsData.totalCount,
        onPageChange: transactionsData.handlePageChange,
        onPageSizeChange: transactionsData.handlePageSizeChange,
        isMobile: isMobile,
        t: transactionsData.t,
      })}
      t={transactionsData.t}
    >
      <QuotaTransactionsTable {...transactionsData} />
    </CardPro>
  );
};

export default QuotaTransactionsPage;
