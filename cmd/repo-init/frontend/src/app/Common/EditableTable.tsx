import React from "react";
import {TableComposable, Tbody, Td, Th, Thead, Tr} from "@patternfly/react-table";
import {TdActionsType} from "@patternfly/react-table/dist/js/components/Table/base";
import {TextInput} from "@patternfly/react-core";
import _ from "lodash";

export interface EditableTableColumn {
  name: string;
  property: string;
}

export interface EditableTableProps {
  columns: EditableTableColumn[];
  onBlur?: () => void;
  collectionName: string;
  data: Array<any> | undefined;
  actions?: TdActionsType | undefined
  lastRowActions?: TdActionsType | undefined
  onChange: (property, val) => void;
}

const EditableTable: React.FunctionComponent<EditableTableProps> = (
  {
    columns,
    onBlur,
    collectionName,
    data,
    actions,
    lastRowActions,
    onChange
  }) => {

  function handleChange(val, evt) {
    onChange(evt.target.name, val)
  }

  const LastRow = () => {
    let rowActions;
    const rowIndex = data ? data.length : 0;

    if (rowIndex == 0) {
      if (lastRowActions) {
        rowActions = (
          <Td key={`${rowIndex}_${columns.length}`}
              actions={lastRowActions}>
          </Td>)
      } else {
        rowActions = (<React.Fragment/>)
      }

      return (<Tr key={rowIndex}>
        {columns.map((col, colIndex) => {
          const column = columns[colIndex];
          const columnValue = collectionName + '[' + rowIndex + '].' + column.property;
          return (<Td key={`${rowIndex}_${colIndex}`} dataLabel={column.name}>
            <TextInput
              name={columnValue}
              id={columnValue}
              onChange={handleChange}
              value=""
              onBlur={onBlur}
            />
          </Td>)
        })}
        {rowActions}
      </Tr>);
    } else {
      return <React.Fragment/>;
    }
  };

  return (
    <TableComposable aria-label="Inputs">
      <Thead>
        <Tr>
          {columns.map((column, columnIndex) => (
            <Th key={columnIndex}>{columns[columnIndex].name}</Th>
          ))}
        </Tr>
      </Thead>
      <Tbody>
        {data?.map((row, rowIndex) => {
          let rowActions;

          if (data !== undefined && data.length > 1 && (rowIndex < data.length - 1) && actions) {
            rowActions = (
              <Td key={`${rowIndex}_${columns.length}`}
                  actions={actions}>
              </Td>)
          } else if (data !== undefined && data.length > 0 && (rowIndex === data.length - 1) && lastRowActions) {
            rowActions = (
              <Td key={`${rowIndex}_${columns.length}`}
                  actions={lastRowActions}>
              </Td>)
          } else {
            rowActions = (<React.Fragment/>)
          }
          return (<Tr key={rowIndex}>
            {columns.map((col, colIndex) => {
              const column = columns[colIndex];
              const columnValue = collectionName + '[' + rowIndex + '].' + column.property;
              return (<Td key={`${rowIndex}_${colIndex}`} dataLabel={column.name}>
                  <TextInput
                    name={columnValue}
                    id={columnValue}
                    value={_.get(row, column.property) || ""}
                    onChange={handleChange}
                  />
                </Td>
              )
            })}
            {rowActions}
          </Tr>)
        })}
        {LastRow()}
      </Tbody>
    </TableComposable>
  );
}

export {EditableTable}
