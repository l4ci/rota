# The skill lanes, shared by skills-docs.py and site-sync.py so the lane rule
# lives in one place. Python 3 stdlib only.
LANES = [
    ('capture', 'Capture'),
    ('plan', 'Plan'),
    ('execute', 'Execute'),
    ('ship', 'Ship'),
    ('persist', 'Persist'),
    ('rounds', 'Rounds'),
    ('maintenance', 'Maintenance'),
]
LANE_NAME = dict(LANES)


def lane_error(rel, lane):
    """The validation message for a page whose lane is not a known one, else None."""
    if lane in LANE_NAME:
        return None
    return '%s: lane %r is not one of %s' % (rel, lane, ', '.join(LANE_NAME))
