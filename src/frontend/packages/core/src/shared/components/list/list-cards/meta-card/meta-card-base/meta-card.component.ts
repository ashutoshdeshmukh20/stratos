import { Component, ContentChild, ContentChildren, Input, OnDestroy, QueryList } from '@angular/core';
import { combineLatest, Observable, of as observableOf, Subscription } from 'rxjs';
import { first, map, tap } from 'rxjs/operators';

import { FavoritesConfigMapper } from '@stratosui/store';
import { EntityMonitorFactory } from '@stratosui/store';
import { MenuItem } from '@stratosui/store';
import { ComponentEntityMonitorConfig, StratosStatus } from '@stratosui/store';
import { IFavoriteMetadata, UserFavorite } from '@stratosui/store';
import { getFavoriteFromEntity } from '@stratosui/store';
import { safeUnsubscribe } from '../../../../../../core/utils.service';
import { MetaCardItemComponent } from '../meta-card-item/meta-card-item.component';
import { MetaCardTitleComponent } from '../meta-card-title/meta-card-title.component';


export function createMetaCardMenuItemSeparator() {
  return {
    label: '-',
    separator: true,
    action: () => { }
  };
}

@Component({
  selector: 'app-meta-card',
  templateUrl: './meta-card.component.html',
  styleUrls: ['./meta-card.component.scss']
})
export class MetaCardComponent implements OnDestroy {

  @ContentChildren(MetaCardItemComponent)
  metaItems: QueryList<MetaCardItemComponent>;

  @ContentChild(MetaCardTitleComponent, { static: true })
  title: MetaCardTitleComponent;

  @Input()
  status$: Observable<StratosStatus>;

  @Input()
  public favorite: UserFavorite<IFavoriteMetadata>;

  @Input()
  public confirmFavoriteRemoval = false;

  @Input()
  statusIcon = true;

  @Input()
  statusIconByTitle = false;

  @Input()
  statusIconTooltip: string;

  @Input()
  statusBackground = false;

  @Input()
  clickAction: () => void = null;

  @Input()
  set entityConfig(entityConfig: ComponentEntityMonitorConfig) {
    if (entityConfig) {
      const entityMonitor = this.entityMonitorFactory.create(
        entityConfig.guid,
        entityConfig.schema
      );
      this.isDeleting$ = entityMonitor.isDeletingEntity$;
      if (!this.favorite) {
        this.entityMonitorSub = entityMonitor.entity$.pipe(
          first(),
          tap(entity => this.favorite = getFavoriteFromEntity(
            entity,
            entityConfig.schema.entityType,
            this.favoritesConfigMapper,
            entityConfig.schema.endpointType
          ))
        ).subscribe();
      }
    }
  }

  @Input('actionMenu')
  set actionMenu(actionMenu: MenuItem[]) {
    if (actionMenu) {
      this.pActionMenu = actionMenu.map(menuItem => {
        if (!menuItem.can) {
          menuItem.separator = menuItem.label === '-';
          menuItem.can = observableOf(!menuItem.separator);
        }
        if (!menuItem.disabled) {
          menuItem.disabled = observableOf(false);
        }
        return menuItem;
      });

      this.showMenu$ = combineLatest(actionMenu.map(menuItem => menuItem.can)).pipe(
        map(cans => cans.some(can => can))
      );
    }
  }
  get actionMenu(): MenuItem[] {
    return this.pActionMenu;
  }

  entityMonitorSub: Subscription;

  public showMenu$: Observable<boolean>;
  public isDeleting$: Observable<boolean> = observableOf(false);
  private pActionMenu: MenuItem[];

  constructor(
    private entityMonitorFactory: EntityMonitorFactory,
    private favoritesConfigMapper: FavoritesConfigMapper,
  ) { }

  ngOnDestroy() {
    safeUnsubscribe(this.entityMonitorSub);
  }
}
