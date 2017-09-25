import { RequestOptions, URLSearchParams } from '@angular/http';
import { schema } from 'normalizr';

import { getAPIResourceGuid } from './api.actions';
import { APIAction, ApiActionTypes } from './api.actions';
import { SpaceSchema } from './space.actions';

export const GET_ALL = '[Application] Get all';
export const GET_ALL_SUCCESS = '[Application] Get all success';
export const GET_ALL_FAILED = '[Application] Get all failed';

export const GET = '[Application] Get one';
export const GET_SUCCESS = '[Application] Get one success';
export const GET_FAILED = '[Application] Get one failed';

// ###### Move these schemas - NJ
export const StackSchema = new schema.Entity('stack', {}, {
    idAttribute: getAPIResourceGuid
});
// ######


export const ApplicationSchema = new schema.Entity('application', {
    entity: {
        stack: StackSchema,
        space: SpaceSchema
    }
}, {
        idAttribute: getAPIResourceGuid
    });

export class GetAllApplications implements APIAction {
    constructor(public paginationKey?: string) {
        this.options = new RequestOptions();
        this.options.url = 'apps';
        this.options.method = 'get';
        this.options.params = new URLSearchParams();
        this.options.params.set('page', '1');
        this.options.params.set('results-per-page', '100');
        this.options.params.set('inline-relations-depth', '1');
    }
    actions = [
        GET_ALL,
        GET_ALL_SUCCESS,
        GET_ALL_FAILED
    ];
    type = ApiActionTypes.API_REQUEST;
    entity = [ApplicationSchema];
    entityKey = ApplicationSchema.key;
    options: RequestOptions;
}

export class GetApplication implements APIAction {
    constructor(public guid: string, public cnis: string) {
        this.options = new RequestOptions();
        this.options.url = `apps/${guid}/summary`;
        this.options.method = 'get';
    }
    actions = [
        GET,
        GET_SUCCESS,
        GET_FAILED
    ];
    type = ApiActionTypes.API_REQUEST;
    entity = [ApplicationSchema];
    entityKey = ApplicationSchema.key;
    options: RequestOptions;
}
