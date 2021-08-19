import * as React from 'react';
import {Alert, Spinner, TreeView, TreeViewDataItem} from '@patternfly/react-core';
import {selectParents, selectTrees, updateNames, updateParents, updateTrees} from "@app/store";
import {useAppDispatch, useAppSelector} from "@app/hook";

export interface SearchTreeViewProps {
  /** URL to fetch raw data from */
  dataUrl: string;
  /** A child ID to render as active at first */
  activeChild?: string;
  /** Set the active item to let others know */
  setActiveItem: React.Dispatch<React.SetStateAction<TreeViewDataItem>>;
}

type Node = {
  name: string;
  field: string;
  children?: Node[];
};

/** withParameter adds a parameter to the source set, doing a deep copy to not mutate the source */
const withParameter = (parameters: URLSearchParams, name: string, field: string): URLSearchParams => {
  const newRecord: URLSearchParams = new URLSearchParams();
  parameters.forEach(function (value: string, key: string) {
    newRecord.set(key, value);
  })
  newRecord.set(field, name);
  return newRecord;
};

/** toId is a deterministic transformation from URL parameters to a string */
export const toId = (parameters: URLSearchParams): string => {
  parameters.sort();
  return parameters.toString();
}

/** toAllItems converts the backend forest response into TreeViewDataItems */
const toAllItems = (data: Node[]): [TreeViewDataItem[], Record<string, string>, Record<string, string>] => {
  const parentsById: Record<string, string> = {};
  const namesById: Record<string, string> = {};
  const items: TreeViewDataItem[] = [];
  const parameters: URLSearchParams = new URLSearchParams();
  for (const datum of data) {
    items.push(toItems(datum, parameters, parentsById, namesById))
  }
  return [items, parentsById, namesById];
}

/** toItems converts an invidual tree in the backend response into our representation */
const toItems = (datum: Node, parameters: URLSearchParams, parentsById: Record<string, string>, namesById: Record<string, string>): TreeViewDataItem => {
  const name: string = datum.name ? datum.name : "<no " + datum.field + ">";
  const uniqueParameters: URLSearchParams = withParameter(parameters, datum.name, datum.field);
  const id: string = toId(uniqueParameters);
  const item: TreeViewDataItem = {
    name: name,
    id: id,
  }
  namesById[id] = name;
  if (datum.children) {
    const items: TreeViewDataItem[] = [];
    for (const child of datum.children) {
      const childItem: TreeViewDataItem = toItems(child, uniqueParameters, parentsById, namesById);
      if (childItem.id) {
        parentsById[childItem.id] = id;
      }
      items.push(childItem)
    }
    item.children = items;
  }
  return item;
}

/** selectTree filters the entire forest to the one selected tree */
const selectTree = (roots: TreeViewDataItem[], ids?: string[]): TreeViewDataItem[] => {
  if (!ids || ids?.length === 0) {
    return roots;
  }
  for (const item of roots) {
    if (item.id === ids[0]) {
      return [selectSubTree(item, ids.slice(1))];
    }
  }
  return roots;
}

/** selectSubTree filters a tree to just the children that are selected */
const selectSubTree = (root: TreeViewDataItem, ids: string[]): TreeViewDataItem => {
  const copied: TreeViewDataItem = {name: undefined};
  Object.assign(copied, root);
  copied.defaultExpanded = true;
  if (root.children && ids.length > 0) {
    for (const child of root.children) {
      if (child.id === ids[0]) {
        copied.children = [selectSubTree(child, ids.slice(1))];
      }
    }
  }
  return copied;
}

/** activeTree unfurls a leaf ID to the full dependency chain of IDs for a DFS */
const activeTree = (parents: Record<string, string>, leafId?: string): string[] => {
  if (!leafId) {
    return [];
  }
  let childId: string = leafId;
  const tree: string[] = [];
  while(childId) {
    tree.unshift(childId);
    childId = parents[childId];
  }
  return tree;
};

export const SearchTreeView: React.FunctionComponent<SearchTreeViewProps> = (
  {
    dataUrl,
    activeChild,
    setActiveItem,
    ...props
  }: SearchTreeViewProps) => {
  const allItems = useAppSelector(selectTrees);
  const parents = useAppSelector(selectParents);
  const dispatch = useAppDispatch();
  const [activeItems, setActiveItems] = React.useState<TreeViewDataItem[]>([]);
  const [filteredItems, setFilteredItems] = React.useState<TreeViewDataItem[]>([]);
  const [isFiltered, setIsFiltered] = React.useState<boolean>(false);
  const [fetchError, setFetchError] = React.useState<string>("");

  // when we first render (or the active child changes), get the data from the backend or our redux store
  React.useEffect(() => {
    let mounted = true;
    if (allItems && allItems[dataUrl] && allItems[dataUrl].length > 0) {
      setFilteredItems(selectTree(allItems[dataUrl], activeTree(parents, activeChild)));
    } else {
      fetch(dataUrl, {headers: {"Accept": "application/json"}}).then(async (res) => {
        if (!res.ok) {
          const raw = await res.text();
          throw new Error(res.status + ": " + raw);
        }
        const raw = await res.json();
        if (!raw) {
          throw new Error("No workloads exist for this type.")
        }
        if (mounted) {
          const [processed, parents, names] = toAllItems(raw);
          const data: Record<string, TreeViewDataItem[]> = {};
          data[dataUrl] = processed;
          dispatch(updateTrees(data));
          dispatch(updateParents(parents));
          dispatch(updateNames(names));
          setFilteredItems(selectTree(processed, activeTree(parents, activeChild)));
        }
      }).catch((error) => {
        if (mounted) {
          setFetchError(String(error));
        }
      })
    }
    return () => {
      mounted = false
    };
  }, [parents, activeChild, dataUrl]); // eslint-disable-line react-hooks/exhaustive-deps

  function onSearch(event: React.ChangeEvent<HTMLInputElement>): void {
    const input = event.target.value;
    if (input.length < 3) {
      setFilteredItems(allItems[dataUrl]);
      setIsFiltered(false);
    } else {
      const filtered: TreeViewDataItem[] = [];
      for (const item of allItems[dataUrl]) {
        const i = filterTree(item, input);
        if (i) {
          filtered.push(i);
        }
      }
      setFilteredItems(filtered);
      setIsFiltered(true);
    }
  }

  function filterTree(item: TreeViewDataItem, input: string): TreeViewDataItem | null {
    const children: TreeViewDataItem[] = [];
    if (item.children) {
      for (const child of item.children) {
        const c = filterTree(child, input);
        if (c) {
          children.push(c);
        }
      }
    }
    const hasMatchingChild: boolean = children.length > 0;
    const matches: boolean = item.name !== undefined && item.name !== null && item.name.toString().toLowerCase().startsWith(input.toLowerCase());
    if (hasMatchingChild || matches) {
      const copied: TreeViewDataItem = {name: undefined};
      Object.assign(copied, item);
      copied.defaultExpanded = true;
      if (hasMatchingChild) {
        copied.children = children;
      }
      return copied;
    }
    return null;
  }

  function onSelect(event: React.MouseEvent, item: TreeViewDataItem, parentItem: TreeViewDataItem): void {
    setActiveItems([item, parentItem]);
    if (!item.children) {
      setActiveItem(item);
    }
  }

  if (fetchError) {
    return <div><Alert variant="danger" title={fetchError}/></div>
  }

  if (filteredItems.length == 0 && !isFiltered) {
    return <div><Spinner isSVG size="xl"/>Loading workloads...</div>
  }

  return <TreeView
    data={filteredItems}
    activeItems={activeItems}
    onSelect={onSelect}
    onSearch={onSearch}
    searchProps={{id: 'input-search', name: 'search-input', 'aria-label': 'Search input example'}}
    {...props}
  />;
};

SearchTreeView.displayName = 'SearchTreeView';
