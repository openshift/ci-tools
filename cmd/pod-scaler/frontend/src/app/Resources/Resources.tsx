import * as React from 'react';
import {
  Breadcrumb,
  BreadcrumbItem,
  Divider,
  Drawer,
  DrawerActions,
  DrawerCloseButton,
  DrawerContent,
  DrawerContentBody,
  DrawerHead,
  DrawerPanelContent,
  Page,
  PageSection,
  PageSectionVariants,
  Text,
  TextContent,
  TreeViewDataItem
} from '@patternfly/react-core';
import {Link, useHistory, useLocation} from "react-router-dom";
import {SearchTreeView, toId} from "@app/SearchTreeView/SearchTreeView";
import {Histograms} from "@app/Histograms/Histograms";
import {selectNames, selectParents} from "@app/store";
import {useAppSelector} from "@app/hook";
import {Location} from "history";
import {BreadcrumbItemRenderArgs} from "@patternfly/react-core/src/components/Breadcrumb/BreadcrumbItem";

interface ResourcesProps {
  urlFragment: string;
  workload: string;
}

export const Resources: React.FunctionComponent<ResourcesProps> = (props: ResourcesProps) => {
  const parents = useAppSelector(selectParents);
  const names = useAppSelector(selectNames);
  const history = useHistory();
  const location: Location = useLocation();
  const [isExpanded, setIsExpanded] = React.useState<boolean>(true);
  const [activeItem, setActiveItem] = React.useState<TreeViewDataItem>();
  const drawerRef: React.RefObject<HTMLDivElement> = React.createRef();

  function onOpenClick() {
    setIsExpanded(true);
  }

  function onCloseClick() {
    setIsExpanded(false);
  }

  React.useEffect(() => {
    if (activeItem && activeItem.id) {
      setIsExpanded(false);
      history.push({
        pathname: location.pathname,
        search: "?" + activeItem.id,
      })
    }
  }, [activeItem]) // eslint-disable-line react-hooks/exhaustive-deps

  function onExpand() {
    drawerRef.current?.focus();
  }

  const panelContent = (
    <DrawerPanelContent>
      <DrawerHead>
                <span tabIndex={isExpanded ? 0 : -1} ref={drawerRef}>
                    <SearchTreeView dataUrl={"/api/indices/" + props.urlFragment}
                                    activeChild={toId(new URLSearchParams(location.search))}
                                    setActiveItem={setActiveItem}/>
                </span>
        <DrawerActions>
          <DrawerCloseButton onClick={onCloseClick}/>
        </DrawerActions>
      </DrawerHead>
    </DrawerPanelContent>
  );

  const breadCrumbFor = (childId: string): React.ReactNode[] => {
    const breadCrumbItems: React.ReactNode[] = [];
    breadCrumbItemsFor(childId, breadCrumbItems)
    return breadCrumbItems;
  }

  const breadCrumbItemsFor = (childId: string, items: React.ReactNode[]) => {
    const name: string = names[childId];
    const link = (props: BreadcrumbItemRenderArgs): React.ReactNode => {
      return <Link to={location.pathname + "?" + childId}
                   onClick={onOpenClick}
                   className={props.className}
                   aria-current={props.ariaCurrent}>{name}</Link>;
    }
    items.unshift(<BreadcrumbItem key={name} isActive={items.length === 0} render={link}/>)
    const parentId: string = parents[childId];
    if (parentId) {
      breadCrumbItemsFor(parentId, items)
    }
  }

  let body: JSX.Element = <div>No selection.</div>;
  const breadcrumbs: React.ReactNode[] = [<BreadcrumbItem key={"root"} isActive={false}
                                                          onClick={onOpenClick}>Select {props.workload}</BreadcrumbItem>];
  if (activeItem && activeItem.id) {
    body = <Histograms dataUrl={"/api/data/" + props.urlFragment} parameters={activeItem.id}/>;
    breadcrumbs.push(...breadCrumbFor(activeItem.id))
  }
  const breadcrumb: React.ReactNode = <Breadcrumb>
    {breadcrumbs}
  </Breadcrumb>;

  return (
    <React.Fragment>
      <Page>
        <PageSection variant={PageSectionVariants.light}>
          <TextContent>
            <Text component="h1">Resource Usage for {props.workload} Workloads</Text>
            <Text component="p">
              Choose a workload to view the CPU and memory usage for recent executions.
            </Text>
          </TextContent>
        </PageSection>
        <Divider component="div"/>
        <PageSection>
          {breadcrumb}
          <Drawer isExpanded={isExpanded} position="left" onExpand={onExpand}>
            <DrawerContent panelContent={panelContent}>
              <DrawerContentBody>
                {body}
              </DrawerContentBody>
            </DrawerContent>
          </Drawer>
        </PageSection>
      </Page>
    </React.Fragment>
  );
};

Resources.displayName = 'Resources';
