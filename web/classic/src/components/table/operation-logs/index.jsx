/*
Copyright (C) 2025 QuantumNous

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
import OperationLogsTable from './OperationLogsTable';
import OperationLogsFilters from './OperationLogsFilters';
import OperationLogsDescription from './OperationLogsDescription';
import { useOperationLogsData } from '../../../hooks/operation-logs/useOperationLogsData';
import { useIsMobile } from '../../../hooks/common/useIsMobile';
import { createCardProPagination } from '../../../helpers/utils';

const OperationLogsPage = () => {
  const logsData = useOperationLogsData();
  const isMobile = useIsMobile();

  return (
    <CardPro
      type='type1'
      descriptionArea={
        <OperationLogsDescription
          compactMode={logsData.compactMode}
          setCompactMode={logsData.setCompactMode}
          t={logsData.t}
        />
      }
      searchArea={<OperationLogsFilters {...logsData} />}
      paginationArea={createCardProPagination({
        currentPage: logsData.activePage,
        pageSize: logsData.pageSize,
        total: logsData.logCount,
        onPageChange: logsData.handlePageChange,
        onPageSizeChange: logsData.handlePageSizeChange,
        isMobile: isMobile,
        t: logsData.t,
      })}
      t={logsData.t}
    >
      <OperationLogsTable {...logsData} />
    </CardPro>
  );
};

export default OperationLogsPage;
