from bustan import Module

from .database import Database


@Module(providers=[Database], exports=[Database])
class DatabaseModule:
    pass
