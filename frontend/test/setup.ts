import { configure } from '@testing-library/react';

// findBy*/waitFor default to 1 s, which full parallel runs on a loaded host
// sometimes exceed for app-level tests that go through several mocked
// requests. A longer ceiling only slows down tests that are failing anyway.
configure({ asyncUtilTimeout: 5000 });
